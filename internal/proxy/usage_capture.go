package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/guardrails"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/telemetry"
	"github.com/agentstation/starport/internal/usage"
)

const (
	// usageCaptureMaxPendingWrites bounds in-flight background record writes.
	// When the bound is reached new records are dropped with a warning; a
	// slow store must never block or fail the request path.
	usageCaptureMaxPendingWrites = 64

	// usageCaptureWriteTimeout bounds one background record write.
	usageCaptureWriteTimeout = 5 * time.Second

	// usageCurrency is the only cost currency Starmap pricing reports.
	usageCurrency = "USD"

	// usageAnonymousKeyID records requests that carried no key identity.
	usageAnonymousKeyID = "anonymous"

	// usageAnonymousAccountID records requests that carried no account. A
	// record has to name an account so the account meter counts it; naming it
	// "anonymous" keeps unauthenticated traffic out of a real account's total
	// while leaving it visible in its own.
	usageAnonymousAccountID = "anonymous"
)

// UsageRecorder persists one usage record. The concept-owned repository in
// internal/usage satisfies it.
type UsageRecorder interface {
	Put(ctx context.Context, record usage.Record) error
}

// UsageObserver sees every record this middleware captures, before the
// asynchronous write. The telemetry seam satisfies it, which is how one
// choke point feeds both the activity store and the scrape.
type UsageObserver interface {
	ObserveUsage(record usage.Record)
}

// UsageCapture is a proxy middleware that records one usage.Record per
// completed inference request. Writes are asynchronous, bounded, and
// best-effort: capture failure never fails or delays a request.
type UsageCapture struct {
	recorder  UsageRecorder
	observers []UsageObserver
	pending   chan struct{}
	group     sync.WaitGroup
}

// NewUsageCapture creates the capture middleware around one recorder. Each
// observer sees every captured record synchronously, so an observer must be
// cheap; counter arithmetic is, a network call is not.
func NewUsageCapture(recorder UsageRecorder, observers ...UsageObserver) *UsageCapture {
	kept := make([]UsageObserver, 0, len(observers))
	for _, observer := range observers {
		if observer != nil {
			kept = append(kept, observer)
		}
	}
	return &UsageCapture{
		recorder:  recorder,
		observers: kept,
		pending:   make(chan struct{}, usageCaptureMaxPendingWrites),
	}
}

// Wrap implements Middleware.
func (c *UsageCapture) Wrap(next Proxy) Proxy {
	return &usageCaptureService{Proxy: next, capture: c}
}

// Flush waits for every in-flight record write to finish.
func (c *UsageCapture) Flush() {
	c.group.Wait()
}

// submit writes one record on a detached bounded background goroutine.
// Observers run first and synchronously: a dropped or failed store write
// still counts on the scrape, so the two surfaces disagree only in the
// direction an operator can live with.
func (c *UsageCapture) submit(record usage.Record) {
	if c == nil {
		return
	}
	for _, observer := range c.observers {
		observer.ObserveUsage(record)
	}
	if c.recorder == nil {
		return
	}
	select {
	case c.pending <- struct{}{}:
	default:
		log.Warn().
			Str("request_id", record.RequestID).
			Msg("usage capture backlog is full; the usage record was dropped")
		return
	}
	c.group.Add(1)
	go func() {
		defer func() {
			<-c.pending
			c.group.Done()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), usageCaptureWriteTimeout)
		defer cancel()
		if err := c.recorder.Put(ctx, record); err != nil {
			log.Warn().
				Err(err).
				Str("request_id", record.RequestID).
				Msg("usage record write failed")
		}
	}()
}

type usageCaptureService struct {
	Proxy
	capture *UsageCapture
}

// ProcessChatCompletion records usage for one completed chat request.
func (s *usageCaptureService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	start := time.Now()
	response, err := s.Proxy.ProcessChatCompletion(ctx, req)

	record := baseUsageRecord(usage.OperationChat, req.RequestID, req.KeyID, req.AccountID, req.TeamID, req.Protocol, req.Request.Model, start)
	record.BatchID = req.BatchID
	applyOutcome(&record, err)
	var snapshot *runtimecatalog.RoutableSnapshot
	if response != nil {
		record.ModelUsed = response.Response.ModelUsed
		record.Provider = response.ProviderUsed
		record.CredentialSource = response.CredentialSource
		record.Attempts = response.Attempts
		record.RoutingMS = response.RoutingDuration.Milliseconds()
		record.CacheStatus = response.CacheStatus
		record.CacheSimilarity, record.CacheSemantic = cacheSimilarity(response.CacheSimilarity)
		record.Tokens = usageTokens(response.Response.Usage)
		record.Media = usageMedia(response.Response.Usage)
		record.GuardrailVerdict = response.GuardrailVerdict
		snapshot = response.CatalogSnapshot
	}
	record.Cost, record.CostUnavailableReason = usageCost(snapshot, record)
	applyExtraction(&record, response)
	if overheadMS, ok := OverheadMS(ctx); ok {
		record.OverheadMS = overheadMS
	}
	telemetry.AnnotateSpanTimings(ctx, record.OverheadMS, record.TTFTMS)
	s.capture.submit(record)
	return response, err
}

// ProcessChatCompletionStream records usage when the routed stream ends.
func (s *usageCaptureService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	start := time.Now()
	stream, err := s.Proxy.ProcessChatCompletionStream(ctx, req)

	record := baseUsageRecord(usage.OperationChat, req.RequestID, req.KeyID, req.AccountID, req.TeamID, req.Protocol, req.Request.Model, start)
	record.BatchID = req.BatchID
	record.Streaming = true
	if err != nil {
		applyOutcome(&record, err)
		record.Cost, record.CostUnavailableReason = usageCost(nil, record)
		if overheadMS, ok := OverheadMS(ctx); ok {
			record.OverheadMS = overheadMS
		}
		s.capture.submit(record)
		return nil, err
	}
	return &usageCaptureStream{
		stream:  stream,
		capture: s.capture,
		record:  record,
		start:   start,
		timer:   execution.OverheadTimerFrom(ctx),
		// The request span outlives the handler return: the server writes the
		// stream inside the handler, so the context still carries the open
		// span when the stream ends and the timings become known.
		requestCtx: ctx,
	}, nil
}

// ProcessEmbeddings records usage for one completed embeddings request.
func (s *usageCaptureService) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	start := time.Now()
	response, err := s.Proxy.ProcessEmbeddings(ctx, req)

	record := baseUsageRecord(usage.OperationEmbeddings, req.RequestID, req.KeyID, req.AccountID, req.TeamID, req.Protocol, req.Request.Model, start)
	record.BatchID = req.BatchID
	applyOutcome(&record, err)
	var snapshot *runtimecatalog.RoutableSnapshot
	if response != nil {
		record.ModelUsed = response.ModelUsed
		record.Provider = response.ProviderUsed
		record.CredentialSource = response.CredentialSource
		record.Attempts = response.Attempts
		record.RoutingMS = response.RoutingDuration.Milliseconds()
		record.CacheStatus = response.CacheStatus
		record.Tokens = usageTokens(response.Response.Usage)
		snapshot = response.CatalogSnapshot
	}
	record.Cost, record.CostUnavailableReason = usageCost(snapshot, record)
	s.capture.submit(record)
	return response, err
}

// ProcessRerank records usage for one completed rerank request.
func (s *usageCaptureService) ProcessRerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error) {
	return captureOperation(ctx, s, usage.OperationRerank, req, req.Request.Model, s.Proxy.ProcessRerank)
}

// ProcessModerations records usage for one completed moderation request.
func (s *usageCaptureService) ProcessModerations(
	ctx context.Context,
	req *ModerationRequest,
) (*ModerationResponse, error) {
	return captureOperation(ctx, s, usage.OperationModerations, req, req.Request.Model, s.Proxy.ProcessModerations)
}

// ProcessImages records usage for one completed image request.
func (s *usageCaptureService) ProcessImages(ctx context.Context, req *ImagesRequest) (*ImagesResponse, error) {
	return captureOperation(ctx, s, usage.OperationImages, req, req.Request.Model, s.Proxy.ProcessImages)
}

// ProcessSpeech records usage for one completed speech request.
func (s *usageCaptureService) ProcessSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	return captureOperation(ctx, s, usage.OperationSpeech, req, req.Request.Model, s.Proxy.ProcessSpeech)
}

// ProcessTranscription records usage for one completed transcription request.
func (s *usageCaptureService) ProcessTranscription(
	ctx context.Context,
	req *TranscriptionRequest,
) (*TranscriptionResponse, error) {
	return captureOperation(ctx, s, usage.OperationTranscription, req, req.Request.Model, s.Proxy.ProcessTranscription)
}

// captureOperation writes one usage record for a media or rerank request.
// Neither answer is cached and neither is streamed, so the record it writes is
// the simplest of the three shapes this file holds: one call, one outcome, one
// submission.
func captureOperation[Request, Response any](
	ctx context.Context,
	s *usageCaptureService,
	operation string,
	req *OperationRequest[Request],
	model string,
	process func(context.Context, *OperationRequest[Request]) (*OperationResponse[Response], error),
) (*OperationResponse[Response], error) {
	start := time.Now()
	response, err := process(ctx, req)

	record := baseUsageRecord(operation, req.RequestID, req.KeyID, req.AccountID, req.TeamID, req.Protocol, model, start)
	applyOutcome(&record, err)
	var snapshot *runtimecatalog.RoutableSnapshot
	if response != nil {
		record.ModelUsed = response.ModelUsed
		record.Provider = response.ProviderUsed
		record.CredentialSource = response.CredentialSource
		record.Attempts = response.Attempts
		record.RoutingMS = response.RoutingDuration.Milliseconds()
		record.Tokens = usageTokens(operationUsage(response.Response))
		record.Media = usageMedia(operationUsage(response.Response))
		record.SearchUnits = int64(operationUsage(response.Response).SearchUnits)
		snapshot = response.CatalogSnapshot
	}
	record.Cost, record.CostUnavailableReason = usageCost(snapshot, record)
	if response != nil {
		// The caller reads the same figure the account is billed, because both
		// come from the one derivation above rather than pricing the turn twice.
		response.Cost = record.Cost
	}
	s.capture.submit(record)
	return response, err
}

// operationUsage reads the units one answer reported. The response types share
// no interface, and adding one for a single field would put a method on every
// canonical type for the benefit of this file alone.
func operationUsage(response any) inference.Usage {
	switch typed := response.(type) {
	case inference.ImagesResponse:
		return typed.Usage
	case inference.SpeechResponse:
		return typed.Usage
	case inference.TranscriptionResponse:
		return typed.Usage
	case inference.RerankResponse:
		return typed.Usage
	case inference.ModerationResponse:
		return typed.Usage
	default:
		return inference.Usage{}
	}
}

// usageCaptureStream latches streamed usage events and records once when the
// stream reaches a terminal state.
type usageCaptureStream struct {
	stream  ChatCompletionStreamResponse
	capture *UsageCapture
	record  usage.Record
	start   time.Time
	timer   *execution.OverheadTimer
	ttft    time.Duration

	// requestCtx carries the request span the timings annotate at finalize.
	requestCtx context.Context

	usage     *inference.Usage
	modelUsed string
	once      sync.Once

	// generatedImages accumulates across deltas. No usage event reports it.
	generatedImages int
}

func (s *usageCaptureStream) Read() (*inference.StreamEvent, error) {
	event, err := s.stream.Read()
	if event != nil {
		if s.ttft == 0 {
			s.ttft = time.Since(s.start)
		}
		if event.Usage != nil {
			latched := *event.Usage
			s.usage = &latched
		}
		s.generatedImages += inference.StreamMediaUnits(event.Deltas).Images
		if event.ModelUsed != "" {
			s.modelUsed = event.ModelUsed
		}
	}
	if err != nil {
		s.finalize(err)
	}
	return event, err
}

func (s *usageCaptureStream) Close() error {
	err := s.stream.Close()
	// Close before a terminal read means the client went away.
	s.finalize(context.Canceled)
	return err
}

// Unwrap exposes the wrapped stream for cross-cutting middleware.
func (s *usageCaptureStream) Unwrap() ChatCompletionStreamResponse {
	return s.stream
}

// GetCacheStatus forwards the inner stream's cache status.
func (s *usageCaptureStream) GetCacheStatus() string {
	if status, ok := s.stream.(CacheStatusProvider); ok {
		return status.GetCacheStatus()
	}
	return ""
}

// GetCacheAge forwards the inner stream's cache age.
func (s *usageCaptureStream) GetCacheAge() int {
	if status, ok := s.stream.(CacheStatusProvider); ok {
		return status.GetCacheAge()
	}
	return 0
}

// GetCacheSimilarity forwards the inner stream's semantic similarity, or
// zero for a stream that served exactly or not from cache at all.
func (s *usageCaptureStream) GetCacheSimilarity() float64 {
	if provider, ok := s.stream.(CacheSimilarityProvider); ok {
		return provider.GetCacheSimilarity()
	}
	return 0
}

// cacheSimilarity reads the two cache fields a record persists from the
// similarity a hit served under. Only the semantic layer reports one, so
// a positive value is the semantic status itself.
func cacheSimilarity(similarity float64) (float64, bool) {
	if similarity <= 0 {
		return 0, false
	}
	return similarity, true
}

func (s *usageCaptureStream) finalize(terminal error) {
	s.once.Do(func() {
		record := s.record
		if errors.Is(terminal, io.EOF) {
			terminal = nil
		}
		applyOutcome(&record, terminal)
		record.LatencyMS = time.Since(s.start).Milliseconds()
		record.ModelUsed = s.modelUsed
		record.CacheStatus = s.GetCacheStatus()
		record.CacheSimilarity, record.CacheSemantic = cacheSimilarity(s.GetCacheSimilarity())
		if verdict := findStreamGuardrailVerdict(s.stream); verdict != "" && record.GuardrailVerdict == "" {
			record.GuardrailVerdict = verdict
		}
		if s.usage != nil {
			latched := *s.usage
			// A streamed answer reports its usage on one chunk and its
			// pictures on others, so the count the deltas carry is the only
			// count there is. Read latches it; the provider never sends it.
			latched.GeneratedImages = s.generatedImages
			record.Tokens = usageTokens(latched)
			record.Media = usageMedia(latched)
			record.TokensEstimated = latched.Estimated
		}
		var snapshot *runtimecatalog.RoutableSnapshot
		if evidence := findStreamEvidence(s.stream); evidence != nil {
			record.Provider = evidence.ProviderUsed()
			record.CredentialSource = evidence.CredentialSourceUsed()
			record.Attempts = evidence.AttemptCount()
			record.RoutingMS = evidence.RoutingDuration().Milliseconds()
			snapshot = evidence.CatalogSnapshot()
		}
		record.Cost, record.CostUnavailableReason = usageCost(snapshot, record)
		if s.timer != nil {
			record.OverheadMS = s.timer.OverheadMS()
		}
		if s.ttft > 0 {
			record.TTFTMS = s.ttft.Milliseconds()
		}
		if s.requestCtx != nil {
			telemetry.AnnotateSpanTimings(s.requestCtx, record.OverheadMS, record.TTFTMS)
		}
		s.capture.submit(record)
	})
}

// findStreamGuardrailVerdict walks the stream wrapper chain to the
// guardrail stream, when one wrapped this turn, and reads its verdict.
func findStreamGuardrailVerdict(stream ChatCompletionStreamResponse) string {
	for stream != nil {
		if provider, ok := stream.(GuardrailVerdictProvider); ok {
			return provider.GuardrailVerdict()
		}
		unwrapper, ok := stream.(StreamUnwrapper)
		if !ok {
			return ""
		}
		stream = unwrapper.Unwrap()
	}
	return ""
}

// findStreamEvidence walks the stream wrapper chain to the routed stream
// that carries route evidence.
func findStreamEvidence(stream ChatCompletionStreamResponse) router.StreamEvidence {
	for stream != nil {
		if evidence, ok := stream.(router.StreamEvidence); ok {
			return evidence
		}
		unwrapper, ok := stream.(StreamUnwrapper)
		if !ok {
			return nil
		}
		stream = unwrapper.Unwrap()
	}
	return nil
}

// A record names the gateway API key, the account behind it, and the team the
// key is attributed to. An operator needs to see which key spent what, an
// account-wide cap needs the sum over every key the account holds, and a
// team-wide cap needs the sum over every attributed key; no identity answers
// for another, so all three travel with the record. The team has no anonymous
// fallback: a teamless request counts toward no team.
func baseUsageRecord(operation, requestID, keyID, accountID, teamID, protocol, modelRequested string, start time.Time) usage.Record {
	if requestID == "" {
		requestID = fallbackRequestID()
	}
	if keyID == "" {
		keyID = usageAnonymousKeyID
	}
	if accountID == "" {
		accountID = usageAnonymousAccountID
	}
	return usage.Record{
		RequestID:      requestID,
		KeyID:          keyID,
		AccountID:      accountID,
		TeamID:         teamID,
		Timestamp:      start,
		Protocol:       protocol,
		Operation:      operation,
		ModelRequested: modelRequested,
	}
}

// applyOutcome sets status, status code, error class, and latency from one
// request outcome. It mirrors the HTTP error mapping in the controllers so
// records match what the client saw.
func applyOutcome(record *usage.Record, err error) {
	record.LatencyMS = time.Since(record.Timestamp).Milliseconds()
	if err == nil {
		record.Status = usage.StatusOK
		record.StatusCode = http.StatusOK
		return
	}
	if errors.Is(err, context.Canceled) {
		record.Status = usage.StatusCancelled
		record.StatusCode = http.StatusRequestTimeout
		record.ErrorClass = string(failure.Canceled)
		return
	}
	var normalized *failure.Failure
	if errors.As(err, &normalized) {
		kind := normalized.Kind()
		record.ErrorClass = string(kind)
		record.StatusCode = usageStatusForKind(kind)
		if kind == failure.Canceled {
			record.Status = usage.StatusCancelled
			return
		}
		record.Status = usage.StatusError
		return
	}
	record.Status = usage.StatusError
	record.ErrorClass = string(failure.Internal)
	record.StatusCode = http.StatusInternalServerError
	var validationError *ValidationError
	var routingError *RoutingError
	var refusal *guardrails.RefusalError
	switch {
	case errors.As(err, &validationError):
		record.ErrorClass = string(failure.Validation)
		record.StatusCode = http.StatusBadRequest
	case errors.As(err, &routingError):
		record.ErrorClass = string(failure.ProviderUnavailable)
		record.StatusCode = http.StatusServiceUnavailable
	case errors.As(err, &refusal):
		record.ErrorClass = guardrailRefusalClass
		record.StatusCode = http.StatusBadRequest
		record.GuardrailVerdict = string(guardrails.VerdictRefuse)
		record.GuardrailCheck = refusal.Check
	}
}

func usageStatusForKind(kind failure.Kind) int {
	switch kind {
	case failure.Validation:
		return http.StatusBadRequest
	case failure.Authentication:
		return http.StatusUnauthorized
	case failure.Permission:
		return http.StatusForbidden
	case failure.Quota, failure.Billing:
		return http.StatusPaymentRequired
	case failure.RateLimit:
		return http.StatusTooManyRequests
	case failure.NotFound:
		return http.StatusNotFound
	case failure.Timeout:
		return http.StatusGatewayTimeout
	case failure.Canceled:
		return http.StatusRequestTimeout
	case failure.ProviderUnavailable:
		return http.StatusServiceUnavailable
	case failure.Unreachable:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func usageTokens(u inference.Usage) usage.Tokens {
	return usage.Tokens{
		Input:      int64(u.InputTokens),
		Output:     int64(u.OutputTokens),
		Total:      int64(u.TotalTokens),
		Reasoning:  int64(u.ReasoningTokens),
		CacheRead:  int64(u.CacheReadTokens),
		CacheWrite: int64(u.CacheWriteTokens),
		// The audio shares are already inside Input and Output. A cost
		// reclassifies them out of the plain rates rather than adding them.
		AudioInput:  int64(u.AudioInputTokens),
		AudioOutput: int64(u.AudioOutputTokens),
	}
}

// usageMedia carries the non-token units of one turn onto its record. It
// returns nil for a text turn, which is what every record written before media
// accounting existed reads back as.
func usageMedia(u inference.Usage) *usage.Media {
	if u.GeneratedImages == 0 {
		return nil
	}
	return &usage.Media{GeneratedImages: int64(u.GeneratedImages)}
}

// applyExtraction folds one turn's document reads onto its usage record.
//
// It runs after the inference cost, because the two are added: an account's
// spend budget meters what the account owes, and a page sent to a recognition
// model is owed whether or not the chat model that read the text ever ran. The
// recognized share stays visible on its own field, so an operator can tell what
// reading the document cost from what answering about it cost.
//
// A record with no inference cost keeps none. The gap the reason names is the
// whole request's cost, and reporting the extraction half alone as the total
// would read as the bill.
func applyExtraction(record *usage.Record, response *ChatCompletionResponse) {
	if response == nil || response.ExtractionEngine == "" {
		return
	}
	record.ParserEngine = response.ExtractionEngine
	record.DocumentPages = int64(response.ExtractionPages)
	record.RecognizedPages = int64(response.RecognizedPages)
	record.NativePages = int64(response.NativePages)
	record.ExtractionCached = response.ExtractionCached
	record.ExtractionMillis = response.ExtractionDuration.Milliseconds()
	if response.ExtractionUnpriced {
		record.Cost = nil
		record.CostUnavailableReason = usage.CostReasonNoPricing
		return
	}
	if response.RecognizedPages == 0 {
		// A native read and a cached read both cost nothing. Reporting a zero
		// cost here would put an extraction cost on a turn that had none.
		return
	}
	record.ExtractionCost = &usage.Cost{NanoUSD: response.ExtractionNanoUSD, Currency: usageCurrency}
	if record.Cost == nil {
		return
	}
	record.Cost.NanoUSD += response.ExtractionNanoUSD
}

// usageCost derives one request's cost from the exact catalog snapshot that
// routed it. A missing cost carries a reason, never a silent zero.
//
// It reads the record rather than the loose counts, because the record already
// holds every unit a turn can be billed in and a fifth unit would otherwise add
// a fifth parameter to every call site.
func usageCost(snapshot *runtimecatalog.RoutableSnapshot, record usage.Record) (*usage.Cost, string) {
	if record.CacheStatus == CacheStatusHit {
		// A cache hit spends no provider tokens.
		return &usage.Cost{NanoUSD: 0, Currency: usageCurrency}, ""
	}
	if snapshot == nil || record.ModelUsed == "" {
		return nil, usage.CostReasonNoRoute
	}
	tokens := record.Tokens
	var units usage.Media
	if record.Media != nil {
		units = *record.Media
	}
	// A generated image, a generated video, and a search unit are each metered
	// per unit, so a turn that produced one reported usage even when it
	// reported no tokens at all.
	noTokens := tokens.Input == 0 && tokens.Output == 0 && tokens.Total == 0
	if noTokens && units.GeneratedImages == 0 && units.GeneratedVideos == 0 && record.SearchUnits == 0 {
		return nil, usage.CostReasonNoUsage
	}
	route, ok := snapshot.ResolveRoute(record.ModelUsed)
	if !ok {
		return nil, usage.CostReasonNoRoute
	}
	offering, err := snapshot.Offering(route)
	if err != nil || offering.Pricing == nil {
		return nil, usage.CostReasonNoPricing
	}
	// The media half decides first. A media unit the offering does not price
	// withdraws the whole cost, because the token half of such a turn is the
	// cheap half and reporting it alone reads as the bill.
	mediaTotal, reason := mediaCost(offering.Pricing, tokens, units)
	if reason != "" {
		return nil, reason
	}
	searchTotal, reason := searchUnitCost(offering.Pricing, record.SearchUnits)
	if reason != "" {
		return nil, reason
	}
	tokenTotal, reason := tokenCost(offering.Pricing.Tokens, tokens)
	if reason != "" {
		return nil, reason
	}
	total := tokenTotal + mediaTotal + searchTotal
	return &usage.Cost{NanoUSD: int64(math.Round(total * 1e9)), Currency: usageCurrency}, ""
}

// searchUnitCost prices the rerank half of one turn.
//
// A turn that reported no search unit costs nothing here, whether it reranked
// nothing or reranked on an offering that bills the tokens it read. The catalog
// projection refuses a rerank offering that publishes no price in the unit it
// names, so an offering this gateway routes always answers; the reason below
// covers a snapshot that reached this point without that guard.
func searchUnitCost(pricing *starmapcatalogs.ModelPricing, searchUnits int64) (float64, string) {
	if searchUnits == 0 {
		return 0, ""
	}
	if pricing.Operations == nil || pricing.Operations.SearchUnit == nil {
		return 0, usage.CostReasonRerankUnpriced
	}
	return float64(searchUnits) * *pricing.Operations.SearchUnit, ""
}

// mediaCost prices the units no plain token rate describes. It answers with a
// cost reason rather than a number when the offering prices none of them. A
// picture billed as free and a spoken minute billed at the text rate are both
// silent understatements, and providers charge many times the text rate for
// audio, so a named gap is the honest answer.
func mediaCost(pricing *starmapcatalogs.ModelPricing, tokens usage.Tokens, units usage.Media) (float64, string) {
	var total float64
	if units.GeneratedImages > 0 {
		if pricing.Operations == nil || pricing.Operations.ImageGen == nil {
			return 0, usage.CostReasonMediaUnpriced
		}
		total += float64(units.GeneratedImages) * *pricing.Operations.ImageGen
	}
	if units.GeneratedVideos > 0 {
		// A video is priced per video, the way an image is. An offering that
		// serves videos and publishes no video price withdraws the whole cost,
		// because a video is the most expensive unit this gateway meters and
		// reporting the token half alone would read as the bill.
		if pricing.Operations == nil || pricing.Operations.VideoGen == nil {
			return 0, usage.CostReasonMediaUnpriced
		}
		total += float64(units.GeneratedVideos) * *pricing.Operations.VideoGen
	}
	audioPriced := pricing.Tokens != nil
	if tokens.AudioInput > 0 && (!audioPriced || pricing.Tokens.AudioInput == nil) {
		return 0, usage.CostReasonMediaUnpriced
	}
	if tokens.AudioOutput > 0 && (!audioPriced || pricing.Tokens.AudioOutput == nil) {
		return 0, usage.CostReasonMediaUnpriced
	}
	return total, ""
}

// tokenCost prices the token half of one turn. It returns zero without a
// reason when the turn reported no tokens, because an image-only answer is
// priced entirely by mediaCost and an offering that generates pictures need
// not publish a token rate at all.
func tokenCost(prices *starmapcatalogs.ModelTokenPricing, tokens usage.Tokens) (float64, string) {
	if tokens.Input == 0 && tokens.Output == 0 && tokens.Total == 0 {
		return 0, ""
	}
	if prices == nil || (prices.Input == nil && prices.Output == nil) {
		return 0, usage.CostReasonNoPricing
	}
	inputRate := modelTokenPrice(prices.Input)
	outputRate := modelTokenPrice(prices.Output)
	readRate := inputRate
	if prices.CacheRead != nil {
		readRate = modelTokenPrice(prices.CacheRead)
	}
	writeRate := inputRate
	if prices.CacheWrite != nil {
		writeRate = modelTokenPrice(prices.CacheWrite)
	}
	// Audio, cache reads, and cache writes are all shares of the totals rather
	// than additions to them. Each one is subtracted from the plain share and
	// added back at its own rate, so the same token is billed exactly once.
	audioInputRate := modelTokenPrice(prices.AudioInput)
	audioOutputRate := modelTokenPrice(prices.AudioOutput)
	uncachedInput := tokens.Input - tokens.CacheRead - tokens.CacheWrite - tokens.AudioInput
	if uncachedInput < 0 {
		uncachedInput = 0
	}
	plainOutput := tokens.Output - tokens.AudioOutput
	if plainOutput < 0 {
		plainOutput = 0
	}
	total := float64(uncachedInput)*inputRate +
		float64(tokens.CacheRead)*readRate +
		float64(tokens.CacheWrite)*writeRate +
		float64(tokens.AudioInput)*audioInputRate +
		float64(plainOutput)*outputRate +
		float64(tokens.AudioOutput)*audioOutputRate
	return total, ""
}

// fallbackRequestID mints a random identifier when the transport supplied
// none, so the record stays addressable.
func fallbackRequestID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "req_unidentified"
	}
	return "req_" + hex.EncodeToString(buffer)
}
