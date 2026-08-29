package controllers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/server/requestctx"
)

const filesNotConfiguredMessage = "File storage is not configured"

// maxFileMemoryBytes bounds what one upload keeps in memory. Whatever passes
// it spills to a temporary file, so a large upload costs disk rather than heap.
// The bound on the upload itself is a deployment setting and arrives with the
// controller.
const maxFileMemoryBytes = 8 << 20

// defaultFileListLimit is the page size an absent limit selects, and
// maxFileListLimit is the largest page one request may ask for.
const (
	defaultFileListLimit = 100
	maxFileListLimit     = 1000
)

// ErrUploadTooLarge reports an upload that reached the configured byte bound.
//
// The bound is enforced on the way in rather than after the read, so a refused
// upload never reaches the byte store and leaves no partial object behind.
var ErrUploadTooLarge = errors.New("upload exceeds the configured byte bound")

// FilesController serves the OpenAI files surface under /v1/files.
//
// It holds no blob key and opens no store. The file service owns both, which
// is why reading content goes through Open rather than through a key this
// controller could log or return.
type FilesController struct {
	service     *files.Service
	uploadBound int64
}

// NewFilesController builds the files adapter over the file service.
func NewFilesController(service *files.Service, uploadBound int64) *FilesController {
	if uploadBound <= 0 {
		uploadBound = 512 << 20
	}
	return &FilesController{service: service, uploadBound: uploadBound}
}

// Create handles POST /v1/files.
func (h *FilesController) Create(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.uploadBound)
	// #nosec G120 -- the line above bounds the body at the configured upload
	// bound, so the parse below cannot read past it whatever the body claims.
	if err := r.ParseMultipartForm(maxFileMemoryBytes); err != nil {
		h.writeUploadError(w, err)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	part, header, err := r.FormFile("file")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest,
			"The request carries no file part")
		return
	}
	defer func() { _ = part.Close() }()

	retention, err := uploadRetention(r)
	if err != nil {
		dto.WriteValidationError(w, expiresAfterSeconds, err.Error())
		return
	}

	// The multipart parse already read the part to memory or to a spill file,
	// so the header states the real size rather than a caller's claim. The
	// service still reconciles it against what the write lands.
	record, err := h.service.Upload(r.Context(), files.UploadRequest{
		Account:          requestctx.AccountIDOrDefault(r.Context()),
		Filename:         header.Filename,
		Purpose:          files.Purpose(strings.TrimSpace(r.FormValue("purpose"))),
		Retention:        retention,
		Size:             header.Size,
		StoredBytesBound: storedBytesBound(r),
	}, part)
	if err != nil {
		h.writeError(w, "upload a file", err)
		return
	}
	_ = dto.WriteJSON(w, http.StatusOK, storedFile(record))
}

// List handles GET /v1/files.
func (h *FilesController) List(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	limit, err := fileListLimit(r.URL.Query().Get("limit"))
	if err != nil {
		dto.WriteValidationError(w, "limit", err.Error())
		return
	}

	records, err := h.service.List(r.Context(), requestctx.AccountIDOrDefault(r.Context()), maxFileListLimit)
	if err != nil {
		h.writeError(w, "list files", err)
		return
	}

	page, hasMore := filePage(records, r.URL.Query().Get("purpose"),
		r.URL.Query().Get("order"), r.URL.Query().Get("after"), limit)
	objects := make([]openai.StoredFile, 0, len(page))
	for _, record := range page {
		objects = append(objects, storedFile(record))
	}
	_ = dto.WriteJSON(w, http.StatusOK, openai.NewStoredFileList(objects, hasMore))
}

// Get handles GET /v1/files/{file_id}.
func (h *FilesController) Get(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	record, err := h.service.Get(r.Context(),
		requestctx.AccountIDOrDefault(r.Context()), chi.URLParam(r, "file_id"))
	if err != nil {
		h.writeError(w, "read a file record", err)
		return
	}
	_ = dto.WriteJSON(w, http.StatusOK, storedFile(record))
}

// Delete handles DELETE /v1/files/{file_id}.
func (h *FilesController) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := chi.URLParam(r, "file_id")
	if err := h.service.Delete(r.Context(), requestctx.AccountIDOrDefault(r.Context()), id); err != nil {
		h.writeError(w, "delete a file", err)
		return
	}
	_ = dto.WriteJSON(w, http.StatusOK, openai.NewStoredFileDeletion(id))
}

// Content handles GET /v1/files/{file_id}/content.
//
// The bytes stream from the store to the client. The controller never holds
// the whole file, because a deployment that answers a 512 MB download by
// buffering it answers two of them by falling over.
func (h *FilesController) Content(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	record, reader, err := h.service.Open(r.Context(),
		requestctx.AccountIDOrDefault(r.Context()), chi.URLParam(r, "file_id"))
	if err != nil {
		h.writeError(w, "read file content", err)
		return
	}
	defer func() { _ = reader.Close() }()

	// The stored name is caller-supplied, so it goes in a quoted filename
	// parameter and never in a header a caller could split.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(record.Bytes, 10))
	w.Header().Set("Content-Disposition", contentDisposition(record.Filename))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, reader); err != nil {
		// The status line is already written, so the only honest report is a
		// log line. A truncated body is what the client observes.
		log.Warn().Err(err).Str("file_id", record.ID).Msg("file content stream ended early")
	}
}

// ready reports whether the deployment configured file storage.
func (h *FilesController) ready(w http.ResponseWriter) bool {
	if h == nil || h.service == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, filesNotConfiguredMessage)
		return false
	}
	return true
}

// writeUploadError separates a body that passed the bound from a body that is
// not a multipart form. The first is a limit the operator set and the second
// is a client mistake, and a caller fixes them differently.
func (h *FilesController) writeUploadError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		dto.WriteError(w, http.StatusRequestEntityTooLarge, dto.ErrorTypeInvalidRequest,
			fmt.Sprintf("%s. This deployment accepts %d bytes at most", ErrUploadTooLarge.Error(), h.uploadBound))
		return
	}
	dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest,
		"The request body is not a multipart form")
}

// storedBytesBound resolves the stored byte bound this upload must satisfy.
// Both the account and the key travel in the request context from
// authentication, so neither read reaches storage on the upload path.
func storedBytesBound(r *http.Request) int64 {
	var accountLimits, keyLimits *limits.Limits
	if record, ok := requestctx.GetAccountRecord(r.Context()); ok && record != nil {
		accountLimits = record.Limits
	}
	if apiKey, ok := requestctx.GetAPIKeyModel(r.Context()); ok && apiKey != nil {
		keyLimits = apiKey.Limits
	}
	rule, bounded := limits.TightestStoredBytes(accountLimits, keyLimits)
	if !bounded {
		return 0
	}
	return rule.Limit
}

// writeError maps a file service failure onto a status.
func (h *FilesController) writeError(w http.ResponseWriter, action string, err error) {
	switch {
	case errors.Is(err, files.ErrFileNotFound):
		// A file another account owns reads the same way as a file that never
		// existed. Any other answer would report that the identifier is real.
		dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "No such File object")
	case errors.Is(err, files.ErrInvalidPurpose):
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, acceptedPurposeMessage())
	case errors.Is(err, files.ErrInvalidFile),
		errors.Is(err, files.ErrRetentionTooLong),
		errors.Is(err, files.ErrRetentionTooShort):
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
	case errors.Is(err, limits.ErrStorageFull):
		// The upload itself is a legal size. What it would not fit inside is
		// the room this holder has left, which a delete frees and a retry of
		// the same request does not. The message says which of the two bounds
		// refused it, because the two need different answers.
		dto.WriteError(w, http.StatusRequestEntityTooLarge, dto.ErrorTypeInvalidRequest,
			"The stored file limit for this account is full. Delete a file to make room.")
	default:
		log.Error().Err(err).Str("action", action).Msg("file request failed")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
			"The file request failed")
	}
}

// acceptedPurposeMessage names the set this gateway serves. The message reads
// the vocabulary rather than restating it, so a purpose added to
// internal/files reaches the error without a second edit.
func acceptedPurposeMessage() string {
	accepted := files.Purposes()
	names := make([]string, 0, len(accepted))
	for _, purpose := range accepted {
		names = append(names, strconv.Quote(string(purpose)))
	}
	return "The purpose is not one this gateway serves. It accepts " + strings.Join(names, " and ")
}

// The expiry form fields. OpenAI nests them, and a multipart form has no
// nesting, so the bracket is part of the field name on the wire.
const (
	expiresAfterAnchor  = "expires_after[anchor]"
	expiresAfterSeconds = "expires_after[seconds]"
)

// anchorCreatedAt is the only anchor this gateway serves. Upstream defines no
// other one, and a gateway that accepted an unknown anchor would apply the
// window from a moment the caller did not mean.
const anchorCreatedAt = "created_at"

// uploadRetention reads the window one upload asked for. An absent field takes
// the window the deployment set.
//
// The service decides whether the window is allowed. This function only turns
// two form fields into a duration, so the bound and its message stay in the one
// package that owns retention.
func uploadRetention(r *http.Request) (time.Duration, error) {
	anchor := strings.TrimSpace(r.FormValue(expiresAfterAnchor))
	raw := strings.TrimSpace(r.FormValue(expiresAfterSeconds))
	if anchor == "" && raw == "" {
		return 0, nil
	}
	if anchor != "" && anchor != anchorCreatedAt {
		return 0, fmt.Errorf("the only anchor this gateway serves is %q", anchorCreatedAt)
	}
	if raw == "" {
		return 0, errors.New("an expiry anchor needs a second count beside it")
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 {
		return 0, errors.New("it must be a whole number of seconds above zero")
	}
	return time.Duration(seconds) * time.Second, nil
}

// storedFile maps one record onto the wire object.
func storedFile(record files.File) openai.StoredFile {
	object := openai.StoredFile{
		ID:        record.ID,
		Object:    openai.StoredFileObject,
		Bytes:     record.Bytes,
		CreatedAt: record.CreatedAt.Unix(),
		Filename:  record.Filename,
		Purpose:   string(record.Purpose),
		Status:    openai.StoredFileStatusProcessed,
	}
	if record.State != files.FileStateReady {
		object.Status = openai.StoredFileStatusUploaded
	}
	if !record.ExpiresAt.IsZero() {
		object.ExpiresAt = record.ExpiresAt.Unix()
	}
	return object
}

// fileListLimit reads the page size one request asked for.
func fileListLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultFileListLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxFileListLimit {
		return 0, fmt.Errorf("it must be a whole number from 1 through %d", maxFileListLimit)
	}
	return limit, nil
}

// filePage orders, filters, and cuts one page.
//
// The order is newest first by default, which is what a caller listing recent
// uploads wants. Two files created in the same second break the tie on the
// identifier, so the sequence a cursor walks is total rather than partial.
func filePage(records []files.File, purpose, order, after string, limit int) ([]files.File, bool) {
	filtered := records
	if purpose = strings.TrimSpace(purpose); purpose != "" {
		filtered = make([]files.File, 0, len(records))
		for _, record := range records {
			if string(record.Purpose) == purpose {
				filtered = append(filtered, record)
			}
		}
	}

	ascending := strings.EqualFold(strings.TrimSpace(order), "asc")
	sort.Slice(filtered, func(i, j int) bool {
		left, right := filtered[i], filtered[j]
		if !left.CreatedAt.Equal(right.CreatedAt) {
			if ascending {
				return left.CreatedAt.Before(right.CreatedAt)
			}
			return right.CreatedAt.Before(left.CreatedAt)
		}
		if ascending {
			return left.ID < right.ID
		}
		return right.ID < left.ID
	})

	if after = strings.TrimSpace(after); after != "" {
		for index, record := range filtered {
			if record.ID == after {
				filtered = filtered[index+1:]
				break
			}
		}
	}

	if len(filtered) > limit {
		return filtered[:limit], true
	}
	return filtered, false
}

// contentDisposition names the download without letting a stored name reach
// the header raw.
//
// The uploader chose the name, so a quote, a backslash, or a newline in it
// would otherwise write a header the caller composed. The plain parameter
// carries the printable ASCII part for an old client, and the RFC 5987
// parameter carries the whole name for a current one.
func contentDisposition(filename string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r >= 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, filename)
	if strings.TrimSpace(ascii) == "" {
		ascii = "file"
	}
	disposition := `attachment; filename="` + ascii + `"`
	if filename != ascii {
		disposition += "; filename*=UTF-8''" + url.PathEscape(filename)
	}
	return disposition
}
