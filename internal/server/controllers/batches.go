package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/requestctx"
)

// batchesNotConfiguredMessage answers a deployment that assembled no batch
// store or no file store. A batch needs both: the record and the JSONL bytes.
const batchesNotConfiguredMessage = "Batch jobs are not configured"

// batchIDParam is the URL parameter the batch routes read.
const batchIDParam = "batch_id"

// maxBatchListLimit bounds one listing, the way the video listing is bounded.
const maxBatchListLimit = 100

// BatchAdmission is the identity one batch line presents to the governor.
// The values are captured at submission, because the line runs long after the
// submitting request and its context are gone.
type BatchAdmission struct {
	AccountID string
	KeyID     string
	// TeamID is the team the submitting key is attributed to, or empty for a
	// teamless key. The governor meters the team's spend budget with it.
	TeamID        string
	AccountLimits *limits.Limits
	KeyLimits     *limits.Limits
}

// BatchGovernor admits one batch line under the account's live budget and
// rate limits. The interface lives here and the meters live in the server
// package, because the meters read middleware-owned state this package never
// holds. A rate refusal waits inside the governor rather than failing the
// line: a batch is background work, and pacing is the point of the limit.
type BatchGovernor interface {
	AdmitLine(ctx context.Context, admission BatchAdmission) error
}

// BatchBudgetError reports a line refused because a budget for the current
// window is exhausted. It is the one governor refusal a caller reads as its
// own doing, so it keeps the online route's 402 shape on the line.
type BatchBudgetError struct {
	Message string
}

// Error states the refusal.
func (e *BatchBudgetError) Error() string { return e.Message }

// BatchesController serves the OpenAI batch surface under /v1/batches.
//
// It holds the batch service for the record lifecycle and the file service
// for the JSONL bytes. The gateway is reached only through the line runner
// this controller hands the service, which is what keeps every provider call
// on the same pipeline the online routes run.
type BatchesController struct {
	*BaseHandler
	batches  *jobs.BatchService
	files    *files.Service
	governor BatchGovernor
}

// NewBatchesController creates an OpenAI-protocol batch controller.
func NewBatchesController(
	service proxy.Proxy,
	batches *jobs.BatchService,
	fileStore *files.Service,
	governor BatchGovernor,
) *BatchesController {
	return &BatchesController{
		BaseHandler: NewProtocolBaseHandler(service, ProtocolOpenAI),
		batches:     batches,
		files:       fileStore,
		governor:    governor,
	}
}

// Create handles POST /v1/batches.
func (h *BatchesController) Create(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	request, err := openai.DecodeBatchCreate(r.Body)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}

	ctx := r.Context()
	account := h.getAccountID(ctx)
	record, err := h.files.Get(ctx, account, request.InputFileID)
	if err != nil {
		if errors.Is(err, files.ErrFileNotFound) {
			// A file another account owns reads the same as one that never
			// existed, exactly as it does on the files surface.
			h.writeBatchStatus(w, http.StatusNotFound, errorTypeNotFound,
				"No stored file matches input_file_id")
			return
		}
		h.logError(ctx, err, "batch input file read failed")
		h.writeBatchStatus(w, http.StatusInternalServerError, errorTypeServer,
			"The batch submission failed")
		return
	}
	if record.Purpose != files.PurposeBatch {
		h.writeBatchStatus(w, http.StatusBadRequest, errorTypeInvalidRequest,
			"The input file was uploaded with purpose "+string(record.Purpose)+
				`. A batch reads a file uploaded with purpose "`+string(files.PurposeBatch)+`"`)
		return
	}

	apiKeyConfig, err := h.getAPIKeyRoutingConfig(ctx)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}

	// The identifier is minted here rather than inside the service, because
	// the line runner has to stamp it on every usage record it draws and the
	// runner is built before the record exists.
	batchID := jobs.NewBatchID()
	runner := &batchLineRunner{
		service:      h.service,
		governor:     h.governor,
		admission:    batchAdmissionFrom(r),
		endpoint:     request.Endpoint,
		batchID:      batchID,
		apiKey:       h.getAPIKey(ctx),
		accountID:    account,
		keyID:        h.getAPIKeyID(ctx),
		teamID:       h.getTeamID(ctx),
		apiKeyConfig: apiKeyConfig,
	}
	batch, err := h.batches.Submit(ctx, jobs.BatchSubmission{
		ID:               batchID,
		Account:          account,
		KeyID:            h.getAPIKeyID(ctx),
		Endpoint:         request.Endpoint,
		InputFileID:      request.InputFileID,
		OutstandingBound: outstandingJobBound(r),
		IO: &batchFileIO{
			files:            h.files,
			account:          account,
			inputFileID:      request.InputFileID,
			storedBytesBound: storedBytesBound(r),
		},
		Runner: runner,
	})
	if err != nil {
		h.writeBatchError(ctx, w, err, "batch submission failed")
		return
	}
	h.writeBatch(w, batch)
}

// List handles GET /v1/batches. It reads records and asks no provider
// anything, so a listing costs one storage read.
func (h *BatchesController) List(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	ctx := r.Context()
	records, err := h.batches.List(ctx, h.getAccountID(ctx), maxBatchListLimit)
	if err != nil {
		h.writeBatchError(ctx, w, err, "batch listing failed")
		return
	}
	published := make([]inference.Batch, 0, len(records))
	for _, record := range records {
		published = append(published, canonicalBatch(record))
	}
	_ = openai.WriteJSON(w, http.StatusOK, openai.EncodeBatches(published))
}

// Get handles GET /v1/batches/{batch_id}.
func (h *BatchesController) Get(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	ctx := r.Context()
	batch, err := h.batches.Get(ctx, h.getAccountID(ctx), chi.URLParam(r, batchIDParam))
	if err != nil {
		h.writeBatchError(ctx, w, err, "batch read failed")
		return
	}
	h.writeBatch(w, batch)
}

// Cancel handles POST /v1/batches/{batch_id}/cancel. Lines already running
// drain and keep their results; no new line starts.
func (h *BatchesController) Cancel(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	ctx := r.Context()
	batch, err := h.batches.Cancel(ctx, h.getAccountID(ctx), chi.URLParam(r, batchIDParam))
	if err != nil {
		h.writeBatchError(ctx, w, err, "batch cancellation failed")
		return
	}
	h.writeBatch(w, batch)
}

// ready reports whether the deployment assembled both stores a batch needs.
func (h *BatchesController) ready(w http.ResponseWriter) bool {
	if h == nil || h.batches == nil || h.files == nil {
		writeVideoRefusal(w, ProtocolOpenAI, http.StatusServiceUnavailable,
			errorTypeServiceUnavailable, batchesNotConfiguredMessage)
		return false
	}
	return true
}

// batchAdmissionFrom captures the submitting caller's identity and limits.
// Both travel in the request context from authentication, so the capture
// reaches no storage.
func batchAdmissionFrom(r *http.Request) BatchAdmission {
	admission := BatchAdmission{
		AccountID: requestctx.AccountIDOrDefault(r.Context()),
	}
	if record, ok := requestctx.GetAccountRecord(r.Context()); ok && record != nil {
		admission.AccountLimits = record.Limits
	}
	if apiKey, ok := requestctx.GetAPIKeyModel(r.Context()); ok && apiKey != nil {
		admission.KeyID = apiKey.ID
		admission.TeamID = apiKey.TeamID
		admission.KeyLimits = apiKey.Limits
	}
	return admission
}

// canonicalBatch projects one record onto the canonical answer.
func canonicalBatch(batch jobs.Batch) inference.Batch {
	answer := inference.Batch{
		ID:             batch.ID,
		Endpoint:       batch.Endpoint,
		InputFileID:    batch.InputFileID,
		OutputFileID:   batch.OutputFileID,
		ErrorFileID:    batch.ErrorFileID,
		State:          string(batch.State),
		Reason:         batch.Reason,
		TotalLines:     batch.TotalLines,
		CompletedLines: batch.CompletedLines,
		FailedLines:    batch.FailedLines,
		CreatedUnix:    batch.CreatedAt.Unix(),
	}
	if !batch.TerminalAt.IsZero() {
		answer.CompletedUnix = batch.TerminalAt.Unix()
	}
	return answer
}

func (h *BatchesController) writeBatch(w http.ResponseWriter, batch jobs.Batch) {
	_ = openai.WriteJSON(w, http.StatusOK, openai.EncodeBatch(canonicalBatch(batch)))
}

// writeBatchError maps a batch service failure onto a status.
func (h *BatchesController) writeBatchError(
	ctx context.Context,
	w http.ResponseWriter,
	err error,
	message string,
) {
	switch {
	case errors.Is(err, jobs.ErrBatchNotFound):
		h.writeBatchStatus(w, http.StatusNotFound, errorTypeNotFound, "No such Batch object")
	case errors.Is(err, jobs.ErrBatchAlreadyEnded),
		errors.Is(err, jobs.ErrInvalidBatch),
		errors.Is(err, jobs.ErrIllegalTransition):
		h.writeBatchStatus(w, http.StatusConflict, errorTypeInvalidRequest, err.Error())
	case errors.Is(err, limits.ErrTooManyOutstandingJobs):
		// The submission is legal. What it would not fit inside is the number
		// of jobs this account already holds open, which a finished batch
		// frees and an immediate retry does not.
		h.writeBatchStatus(w, http.StatusTooManyRequests, errorTypeRateLimit, err.Error())
	default:
		h.logError(ctx, err, message)
		h.writeError(w, err)
	}
}

func (h *BatchesController) writeBatchStatus(
	w http.ResponseWriter,
	status int,
	errorType, message string,
) {
	openai.WriteError(w, status, errorType, message, nil)
}

// batchFileIO adapts the file service to the batch's two file needs. It is
// built per submission, because it closes over one account and one input file.
type batchFileIO struct {
	files            *files.Service
	account          string
	inputFileID      string
	storedBytesBound int64
}

// OpenInput opens the stored input file for one full read.
func (b *batchFileIO) OpenInput(ctx context.Context) (io.ReadCloser, error) {
	_, reader, err := b.files.Open(ctx, b.account, b.inputFileID)
	return reader, err
}

// StoreOutput stores one result file under the purpose no upload may claim.
// The size is unknown while the lines stream, so it lands as zero and the
// service reconciles it against what the write actually stored.
func (b *batchFileIO) StoreOutput(ctx context.Context, name string, content io.Reader) (string, error) {
	record, err := b.files.Upload(ctx, files.UploadRequest{
		Account:          b.account,
		Filename:         name,
		Purpose:          files.PurposeBatchOutput,
		StoredBytesBound: b.storedBytesBound,
	}, content)
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

// batchLineRunner executes one input line on the same pipeline the online
// route runs. It owns the codec calls and the failure shape, so the jobs
// package never learns what a line says.
type batchLineRunner struct {
	service      proxy.Proxy
	governor     BatchGovernor
	admission    BatchAdmission
	endpoint     string
	batchID      string
	apiKey       string
	accountID    string
	keyID        string
	teamID       string
	apiKeyConfig *proxy.APIKeyRoutingConfig
}

// RunLine decodes, admits, executes, and encodes one line. Every failure
// answers through the encoded line rather than an error, because a failed
// line belongs in the error file and the batch keeps going.
func (r *batchLineRunner) RunLine(ctx context.Context, _ int, line []byte) ([]byte, bool) {
	requestID := uuid.NewString()
	decoded, err := openai.DecodeBatchLine(line, r.endpoint)
	if err != nil {
		return r.failureLine(bestEffortCustomID(line), requestID,
			http.StatusBadRequest, errorTypeInvalidRequest, err.Error(), nil), true
	}

	if r.governor != nil {
		if err := r.governor.AdmitLine(ctx, r.admission); err != nil {
			var budget *BatchBudgetError
			if errors.As(err, &budget) {
				return r.failureLine(decoded.CustomID, requestID,
					http.StatusPaymentRequired, errorTypePermission, budget.Message, nil), true
			}
			return r.failureLine(decoded.CustomID, requestID,
				http.StatusInternalServerError, errorTypeServer, err.Error(), nil), true
		}
	}

	body, err := r.execute(ctx, requestID, decoded.Body)
	if err != nil {
		status, errorType, message, param := errorShape(err)
		return r.failureLine(decoded.CustomID, requestID, status, errorType, message, param), true
	}

	encoded, err := openai.EncodeBatchOutputLine(inference.BatchLineResult{
		CustomID:   decoded.CustomID,
		StatusCode: http.StatusOK,
		RequestID:  requestID,
		Body:       body,
	})
	if err != nil {
		return r.failureLine(decoded.CustomID, requestID,
			http.StatusInternalServerError, errorTypeServer, err.Error(), nil), true
	}
	return encoded, false
}

// execute runs one line body through the gateway operation its batch named.
// Streaming is forced off: a result line carries one JSON body, not a stream.
func (r *batchLineRunner) execute(
	ctx context.Context, requestID string, body []byte,
) ([]byte, error) {
	switch r.endpoint {
	case "/v1/embeddings":
		decoded, err := openai.DecodeEmbedding(bytes.NewReader(body))
		if err != nil {
			return nil, lineBodyRefusal(err)
		}
		response, err := r.service.ProcessEmbeddings(ctx, &proxy.EmbeddingsRequest{
			Request:      decoded,
			BatchID:      r.batchID,
			APIKey:       r.apiKey,
			AccountID:    r.accountID,
			KeyID:        r.keyID,
			TeamID:       r.teamID,
			APIKeyConfig: r.apiKeyConfig,
			RequestID:    requestID,
			Protocol:     string(ProtocolOpenAI),
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(openai.EncodeEmbedding(response.Response))

	case "/v1/responses":
		decoded, err := openai.DecodeResponses(bytes.NewReader(body))
		if err != nil {
			return nil, lineBodyRefusal(err)
		}
		response, err := r.chat(ctx, requestID, decoded)
		if err != nil {
			return nil, err
		}
		return json.Marshal(openai.EncodeResponses(response))

	default:
		decoded, err := openai.DecodeChat(bytes.NewReader(body))
		if err != nil {
			return nil, lineBodyRefusal(err)
		}
		response, err := r.chat(ctx, requestID, decoded)
		if err != nil {
			return nil, err
		}
		return json.Marshal(openai.EncodeChat(response))
	}
}

func (r *batchLineRunner) chat(
	ctx context.Context, requestID string, decoded inference.ChatRequest,
) (inference.ChatResponse, error) {
	decoded.Stream = false
	response, err := r.service.ProcessChatCompletion(ctx, &proxy.ChatCompletionRequest{
		Request:      decoded,
		BatchID:      r.batchID,
		APIKey:       r.apiKey,
		AccountID:    r.accountID,
		KeyID:        r.keyID,
		TeamID:       r.teamID,
		APIKeyConfig: r.apiKeyConfig,
		RequestID:    requestID,
		Protocol:     string(ProtocolOpenAI),
	})
	if err != nil {
		return inference.ChatResponse{}, err
	}
	return response.Response, nil
}

// failureLine encodes one failed line in the online route's error shape. The
// body carries the same envelope a direct call would read, so a caller's
// error handling works unchanged on a batch result.
func (r *batchLineRunner) failureLine(
	customID, requestID string,
	status int,
	errorType, message string,
	param *string,
) []byte {
	body, err := json.Marshal(openai.ErrorResponse{
		Error: openai.ErrorDetail{Message: message, Type: errorType, Param: param},
	})
	if err != nil {
		body = []byte(`{"error":{"message":"The batch line failed","type":"server_error"}}`)
	}
	encoded, err := openai.EncodeBatchOutputLine(inference.BatchLineResult{
		CustomID:   customID,
		StatusCode: status,
		RequestID:  requestID,
		Body:       body,
	})
	if err != nil {
		return []byte(`{"error":{"message":"The batch line failed","type":"server_error"}}`)
	}
	return encoded
}

// lineBodyRefusal turns a line-body decode failure into the 400 the online
// route would answer. A stored-state Responses field keeps the param that
// names the feature to drop.
func lineBodyRefusal(err error) error {
	var unsupported *openai.UnsupportedError
	if errors.As(err, &unsupported) {
		return &proxy.ValidationError{Field: unsupported.Param, Message: unsupported.Message}
	}
	return &proxy.ValidationError{Field: "body", Message: err.Error()}
}

// bestEffortCustomID reads the custom_id of a line whose strict decode
// failed, so the error file still names the request when the line is JSON at
// all. A line that is not JSON reads back with an empty one.
func bestEffortCustomID(line []byte) string {
	var probe struct {
		CustomID string `json:"custom_id"`
	}
	_ = json.Unmarshal(line, &probe)
	return probe.CustomID
}
