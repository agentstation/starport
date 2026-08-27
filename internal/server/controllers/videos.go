package controllers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/routing"
)

// videosNotConfiguredMessage answers a deployment that assembled no job store.
const videosNotConfiguredMessage = "Video jobs are not configured"

// videoIDParam is the URL parameter both published families spell the same way.
const videoIDParam = "video_id"

// maxVideoListLimit bounds one listing. A caller reads its own jobs, and a
// tenant that submitted more than this reads the newest of them.
const maxVideoListLimit = 100

// VideosController serves the video job surface.
//
// It holds the job service rather than calling the gateway directly, because
// the record is what a caller comes back to and the service is the only thing
// that writes a state. The gateway reaches a provider only through the runner
// this controller hands the service, which is what keeps the provider job
// identifier inside internal/jobs.
type VideosController struct {
	*BaseHandler
	jobs *jobs.Service
}

// NewVideosController creates an OpenAI-protocol video job controller.
func NewVideosController(service proxy.Proxy, records *jobs.Service) *VideosController {
	return &VideosController{
		BaseHandler: NewProtocolBaseHandler(service, ProtocolOpenAI),
		jobs:        records,
	}
}

// NewOpenRouterVideosController creates an OpenRouter-protocol controller.
func NewOpenRouterVideosController(service proxy.Proxy, records *jobs.Service) *VideosController {
	return &VideosController{
		BaseHandler: NewProtocolBaseHandler(service, ProtocolOpenRouter),
		jobs:        records,
	}
}

// Submit handles POST /v1/videos and POST /api/v1/videos.
func (h *VideosController) Submit(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMediaUploadBytes)
	request, err := h.decodeSubmission(r)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}
	ctx := r.Context()
	gateway, err := mediaGatewayRequest(ctx, h.BaseHandler, request)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	job, err := h.jobs.Submit(ctx, h.service.VideoJobRunner(gateway),
		h.getTenantID(ctx), routing.OperationVideosGenerations)
	if err != nil {
		h.writeJobError(ctx, w, err, "video job submission failed")
		return
	}
	h.writeJob(w, job)
}

// Get handles GET /v1/videos/{video_id}.
//
// The read reaches a provider only while the answer can still change, which the
// job service decides. A caller may poll a finished job as often as it likes
// and reach no provider at all.
func (h *VideosController) Get(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	ctx := r.Context()
	runner, err := h.runner(ctx)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	job, err := h.jobs.Refresh(ctx, runner, h.getTenantID(ctx), chi.URLParam(r, videoIDParam))
	if err != nil {
		h.writeJobError(ctx, w, err, "video job poll failed")
		return
	}
	h.writeJob(w, job)
}

// List handles GET /v1/videos. It reads records and asks no provider anything,
// so a listing costs one storage read however many jobs are still running.
func (h *VideosController) List(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	ctx := r.Context()
	records, err := h.jobs.List(ctx, h.getTenantID(ctx), maxVideoListLimit)
	if err != nil {
		h.writeJobError(ctx, w, err, "video job listing failed")
		return
	}
	published := make([]inference.VideoJob, 0, len(records))
	for _, record := range records {
		if record.Operation == routing.OperationVideosGenerations {
			published = append(published, canonicalVideoJob(record))
		}
	}
	h.writeJobList(w, published)
}

// Cancel handles POST /v1/videos/{video_id}/cancel.
func (h *VideosController) Cancel(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	ctx := r.Context()
	runner, err := h.runner(ctx)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	job, err := h.jobs.Cancel(ctx, runner, h.getTenantID(ctx), chi.URLParam(r, videoIDParam))
	if err != nil {
		h.writeJobError(ctx, w, err, "video job cancellation failed")
		return
	}
	h.writeJob(w, job)
}

// Content handles GET /v1/videos/{video_id}/content.
//
// The route serves Starport's own stored bytes and never redirects a caller to
// the provider. A provider link expires on the provider's schedule and carries
// the provider's credential, so a caller holding a Starport identifier would be
// handed something it cannot read and this gateway cannot promise.
//
// The route reads the record first, so a job another account owns answers not
// found here exactly as it does on every other video path.
func (h *VideosController) Content(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	ctx := r.Context()
	job, asset, err := h.jobs.Open(ctx, h.getTenantID(ctx), chi.URLParam(r, videoIDParam))
	if err != nil {
		h.writeAssetError(ctx, w, err, job)
		return
	}
	defer func() { _ = asset.Close() }()
	w.Header().Set("Content-Type", job.AssetContentType)
	if job.AssetBytes > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(job.AssetBytes, 10))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, asset); err != nil {
		// The status is written. Reporting the failure to the caller is no
		// longer possible, so the log is the only place it can go.
		h.logError(ctx, err, "video content write failed")
	}
}

// writeAssetError separates the two answers a caller reads about missing bytes.
//
// An asset that expired answers 410 and states the window, because the caller
// asked in time for a job that ran and too late for the answer. An asset that
// never arrived answers 404, which is what a running job, a failed job, and a
// fetch that has not landed all read as.
func (h *VideosController) writeAssetError(
	ctx context.Context,
	w http.ResponseWriter,
	err error,
	job jobs.Job,
) {
	switch {
	case errors.Is(err, jobs.ErrAssetExpired):
		h.writeVideoStatus(w, http.StatusGone, errorTypeInvalidRequest,
			"The content for video "+job.ID+" expired. This gateway keeps a finished video for "+
				retentionWindowText(h.jobs.Retention())+" after it stores it.")
	case errors.Is(err, jobs.ErrAssetNotFound):
		h.writeVideoStatus(w, http.StatusNotFound, errorTypeNotFound,
			"No stored content for video "+job.ID)
	default:
		h.writeJobError(ctx, w, err, "video content read failed")
	}
}

// retentionWindowText states one window in its shortest exact form. A duration
// prints every unit it holds, and a caller reading why its content went reads a
// day rather than "24h0m0s".
func retentionWindowText(window time.Duration) string {
	text := window.String()
	switch {
	case strings.HasSuffix(text, "h0m0s"):
		return strings.TrimSuffix(text, "0m0s")
	case strings.HasSuffix(text, "m0s"):
		return strings.TrimSuffix(text, "0s")
	}
	return text
}

// ready reports whether the deployment assembled a job store.
func (h *VideosController) ready(w http.ResponseWriter) bool {
	if h == nil {
		writeVideoRefusal(w, ProtocolOpenAI, http.StatusServiceUnavailable,
			errorTypeServiceUnavailable, videosNotConfiguredMessage)
		return false
	}
	if h.jobs == nil {
		h.writeVideoStatus(w, http.StatusServiceUnavailable,
			errorTypeServiceUnavailable, videosNotConfiguredMessage)
		return false
	}
	return true
}

// runner binds the gateway to this caller for one poll or one cancel. The
// submission it carries is empty: both calls name the work by the handle the
// record supplies, and neither reads a prompt.
func (h *VideosController) runner(ctx context.Context) (jobs.Runner, error) {
	gateway, err := mediaGatewayRequest(ctx, h.BaseHandler, inference.VideoJobRequest{})
	if err != nil {
		return nil, err
	}
	return h.service.VideoJobRunner(gateway), nil
}

// canonicalVideoJob projects one record onto the canonical answer. The record
// holds a provider job identifier and this value does not, which is what lets
// each codec encode it without a field-by-field review.
func canonicalVideoJob(job jobs.Job) inference.VideoJob {
	answer := inference.VideoJob{
		ID:          job.ID,
		Model:       job.Model,
		Provider:    job.Provider,
		State:       string(job.State),
		Reason:      job.Reason,
		CreatedUnix: job.CreatedAt.Unix(),
	}
	if !job.TerminalAt.IsZero() {
		answer.CompletedUnix = job.TerminalAt.Unix()
	}
	return answer
}

func (h *VideosController) decodeSubmission(r *http.Request) (inference.VideoJobRequest, error) {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.DecodeVideoJob(r.Body)
	}
	return openai.DecodeVideoJob(r.Body)
}

func (h *VideosController) writeJob(w http.ResponseWriter, job jobs.Job) {
	answer := canonicalVideoJob(job)
	if h.protocol == ProtocolOpenRouter {
		_ = openrouter.WriteJSON(w, http.StatusOK, openrouter.EncodeVideoJob(answer))
		return
	}
	_ = openai.WriteJSON(w, http.StatusOK, openai.EncodeVideoJob(answer))
}

func (h *VideosController) writeJobList(w http.ResponseWriter, records []inference.VideoJob) {
	if h.protocol == ProtocolOpenRouter {
		_ = openrouter.WriteJSON(w, http.StatusOK, openrouter.EncodeVideoJobs(records))
		return
	}
	_ = openai.WriteJSON(w, http.StatusOK, openai.EncodeVideoJobs(records))
}

// writeJobError maps a job service failure onto a status.
func (h *VideosController) writeJobError(
	ctx context.Context,
	w http.ResponseWriter,
	err error,
	message string,
) {
	switch {
	case errors.Is(err, jobs.ErrJobNotFound):
		// A job another account owns reads the same way as one that never
		// existed. Any other answer would report that the identifier is real,
		// and an identifier is the only thing a caller has to guess.
		h.writeVideoStatus(w, http.StatusNotFound, errorTypeNotFound, "No such video job")
	case errors.Is(err, jobs.ErrJobAlreadyEnded):
		h.writeVideoStatus(w, http.StatusConflict, errorTypeInvalidRequest, err.Error())
	case errors.Is(err, jobs.ErrInvalidJob), errors.Is(err, jobs.ErrIllegalTransition):
		h.writeVideoStatus(w, http.StatusConflict, errorTypeInvalidRequest, err.Error())
	default:
		h.logError(ctx, err, message)
		h.writeError(w, err)
	}
}

func (h *VideosController) writeVideoStatus(
	w http.ResponseWriter,
	status int,
	errorType, message string,
) {
	writeVideoRefusal(w, h.protocol, status, errorType, message)
}

// writeVideoRefusal answers in the dialect the route belongs to. A deployment
// with no job store answers before a controller exists to hold a protocol, so
// the dialect is a parameter rather than a field read.
func writeVideoRefusal(
	w http.ResponseWriter,
	protocol Protocol,
	status int,
	errorType, message string,
) {
	if protocol == ProtocolOpenRouter {
		openrouter.WriteError(w, status, message,
			map[string]any{openRouterErrorTypeField: errorType})
		return
	}
	openai.WriteError(w, status, errorType, message, nil)
}
