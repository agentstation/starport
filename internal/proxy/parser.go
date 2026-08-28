package proxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/document"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/router"
)

// The document parser turns an attached document into text before the chat
// model reads it. Two engines do the work, and the boundary between them is
// who pays: the native engine reads a text layer in this process for nothing,
// and the recognition engine sends a page that carries no text layer to a
// model the catalog serves that operation for.
//
// The order the steps run in is the part worth stating. The route is planned
// from the request the caller sent, and the parser rewrites the request the
// provider receives. A plugin therefore changes what the model reads and never
// which model reads it, which is the invariant that keeps the same prompt
// reaching the same provider with the plugin on and off.

// parseDocuments returns the request the provider receives, with every
// attached document replaced by its text.
//
// A request that named no parser is returned untouched. Attaching a document
// and naming no engine means the caller wants the model itself to read the
// file, and rewriting it would take that choice away.
func (p *proxy) parseDocuments(
	ctx context.Context,
	req *ChatCompletionRequest,
	policy *router.APIKeyConfig,
) (*ChatCompletionRequest, parseReport, error) {
	engine := req.Request.DocumentParser.Engine
	if !req.Request.DocumentParser.Requested() || !carriesDocument(req.Request.Messages) {
		return req, parseReport{}, nil
	}
	if !engine.Known() {
		// A codec refuses an unknown engine before the request reaches here.
		// This arm is what keeps that true of a caller that reaches the proxy
		// through another protocol later.
		return nil, parseReport{}, &ValidationError{
			Field:   "plugins",
			Message: fmt.Sprintf("unknown parser engine %q", engine),
		}
	}

	started := time.Now()
	report := parseReport{Cached: true, Engine: engine}
	parsed := *req
	parsed.Request = req.Request.Clone()
	for messageIndex := range parsed.Request.Messages {
		content := parsed.Request.Messages[messageIndex].Content
		for partIndex := range content {
			part := &content[partIndex]
			if part.Kind != inference.ContentDocument || part.Document == nil {
				continue
			}
			reading, err := p.readDocument(ctx, &parsed, *part.Document, engine, policy)
			if err != nil {
				return nil, parseReport{}, err
			}
			report.add(reading)
			report.charge(p.catalogPrices(ctx), reading)
			*part = inference.ContentPart{
				Kind: inference.ContentText,
				Text: renderDocument(part.Document.Filename, reading.Text),
			}
		}
	}
	report.Duration = time.Since(started)
	return &parsed, report, nil
}

// parseReport is what one turn's document reads cost.
//
// The cached flag answers the question a caller asks, which is whether this
// turn paid to read its attachments. A turn that read one document fresh and
// one from the cache paid, so it reports no hit. With the single attachment
// almost every request carries, the two readings are the same.
type parseReport struct {
	// Documents is how many attachments the parser replaced with text.
	Documents int
	// Pages is how many pages those attachments held.
	Pages int
	// Cached reports that every attachment came back from the cache.
	Cached bool
	// Engine is the engine the caller named.
	Engine inference.ParserEngine
	// RecognizedPages is how many pages this turn sent to a recognition model.
	// A cached read reports none: an earlier turn paid for those pages.
	RecognizedPages int
	// NativePages is how many pages this turn read in process, for nothing.
	NativePages int
	// Offering is the recognition model that read the last document this turn
	// recognized. It is empty when the turn recognized nothing.
	Offering string
	// CostNanoUSD is what the recognized pages cost.
	CostNanoUSD int64
	// Unpriced reports a recognized page the catalog gave no price for. The
	// projection refuses such an offering, so this states a gap rather than a
	// free page.
	Unpriced bool
	// Duration is how long the reads took, the cache lookups included.
	Duration time.Duration
}

func (r *parseReport) add(reading documentReading) {
	r.Documents++
	r.Pages += reading.Pages
	r.Cached = r.Cached && reading.Cached
	switch {
	case reading.Cached:
		// Neither meter moves. The pages were read on an earlier turn, and
		// counting them again here would bill a saving as a spend.
	case reading.Offering != "":
		r.RecognizedPages += reading.Pages
		r.Offering = reading.Offering
	default:
		r.NativePages += reading.Pages
	}
}

// report copies what the reads cost onto the answer the turn returns. It is
// the one place the two vocabularies meet, so the usage middleware reads a
// response rather than a parser type.
func (r parseReport) report(response *ChatCompletionResponse) {
	if response == nil {
		return
	}
	response.ExtractionCached = r.Cached
	if r.Documents == 0 {
		// A turn that attached nothing reports nothing. Without this it would
		// report a cached read of no documents, which is true and misleading.
		response.ExtractionCached = false
		return
	}
	response.ExtractionEngine = string(r.Engine)
	response.ExtractionPages = r.Pages
	response.RecognizedPages = r.RecognizedPages
	response.NativePages = r.NativePages
	response.ExtractionOffering = r.Offering
	response.ExtractionNanoUSD = r.CostNanoUSD
	response.ExtractionUnpriced = r.Unpriced
	response.ExtractionDuration = r.Duration
}

// charge prices one recognized reading against the catalog that routed it.
//
// The price comes from the offering the planner actually chose, so the number
// the record carries is the number the provider charges. A native read and a
// cached read both skip this: neither reached a provider.
func (r *parseReport) charge(prices catalogPrices, reading documentReading) {
	if reading.Cached || reading.Offering == "" || reading.Pages == 0 {
		return
	}
	if prices == nil {
		r.Unpriced = true
		return
	}
	price, priced := prices.PagePriceFor(reading.Offering,
		starmapcatalogs.ProviderOperationDocumentsRecognition)
	if !priced {
		r.Unpriced = true
		return
	}
	r.CostNanoUSD += nanoUSD(float64(reading.Pages) * price)
}

// documentReading is one attachment after the named engine read it.
type documentReading struct {
	document.Reading
	// Cached reports that this text came back from the cache rather than from
	// a read this turn paid for.
	Cached bool
}

// readDocument reads one attached document with the engine the caller named.
//
// The native read always runs first, even when the caller asked for
// recognition. It is what reports how many pages the document holds and which
// of them carry no text, and both facts are needed before a page is worth
// paying to recognize.
func (p *proxy) readDocument(
	ctx context.Context,
	req *ChatCompletionRequest,
	attached inference.Document,
	engine inference.ParserEngine,
	policy *router.APIKeyConfig,
) (documentReading, error) {
	data, mediaType, err := documentBytes(attached)
	if err != nil {
		return documentReading{}, &ValidationError{Field: fieldMessages, Message: err.Error()}
	}

	// The lookup comes before the native read, not only before the paid one.
	// A hit under the recognition engine has already answered whether these
	// bytes needed recognition, so re-reading the text layer to ask again
	// would spend the work the entry exists to avoid.
	key := document.CacheKey{
		AccountID:   req.TenantID,
		ContentHash: document.ContentHash(data),
		Engine:      string(engine),
		Generation:  p.catalogGeneration(ctx),
	}
	if cached, found := p.cachedReading(ctx, key); found {
		return cached, nil
	}

	extraction, err := p.extractor().Extract(ctx, document.Input{
		Data:     data,
		Format:   mediaType,
		Filename: attached.Filename,
	})
	if err != nil {
		// Every refusal the native engine raises is a statement about the
		// bytes the caller attached, so it reaches the caller as one.
		return documentReading{}, &ValidationError{Field: fieldMessages, Message: err.Error()}
	}

	reading := documentReading{Reading: document.Reading{
		Text:  extraction.Text,
		Pages: extraction.PageCount(),
	}}
	if engine == inference.ParserEngineRecognition && needsRecognition(extraction) {
		if err := p.affordable(ctx, attached.Filename, extraction.PageCount()); err != nil {
			return documentReading{}, err
		}
		recognized, offering, err := p.recognize(ctx, req, attached, data, mediaType, extraction, policy)
		if err != nil {
			return documentReading{}, err
		}
		reading.Text = recognized
		reading.Offering = offering
	}
	p.storeReading(ctx, key, reading.Reading)
	return reading, nil
}

// cachedReading answers from the extraction cache when it can.
//
// A cache that cannot answer is not a failure of the request. An unreachable
// store, a record this schema cannot read, and a key with no catalog
// generation behind it all mean the same thing here: read the document. The
// alternative is a gateway whose document turns stop working when its cache
// does.
func (p *proxy) cachedReading(ctx context.Context, key document.CacheKey) (documentReading, bool) {
	if p.extractions == nil {
		return documentReading{}, false
	}
	reading, found, err := p.extractions.Get(ctx, key)
	if err != nil || !found {
		if err != nil {
			log.Debug().Err(err).Msg("extraction cache lookup failed")
		}
		return documentReading{}, false
	}
	return documentReading{Reading: reading, Cached: true}, true
}

// storeReading records what an engine read, and never fails the turn for it.
// The text already reached the caller's model, so a write that did not land
// costs the next identical request one read.
func (p *proxy) storeReading(ctx context.Context, key document.CacheKey, reading document.Reading) {
	if p.extractions == nil {
		return
	}
	if err := p.extractions.Put(ctx, key, reading); err != nil {
		log.Debug().Err(err).Msg("extraction cache write failed")
	}
}

// affordable refuses a recognition call the holder cannot pay for, before the
// call happens.
//
// The estimate uses the cheapest page price the catalog publishes, not the
// price of the offering the planner will choose, because the planner chooses
// after this runs. Erring low is the right direction: a bound that refused work
// the account could have paid for would cost a caller a request over a price
// that was never charged. Erring low overshoots the bound by at most one
// document, and the account's own cap refuses the request after it.
func (p *proxy) affordable(ctx context.Context, filename string, pages int) error {
	allowance := limits.AllowanceFromContext(ctx)
	if !allowance.Bounded || pages == 0 {
		return nil
	}
	prices := p.catalogPrices(ctx)
	if prices == nil {
		return nil
	}
	price, priced := prices.LowestPagePrice(starmapcatalogs.ProviderOperationDocumentsRecognition)
	if !priced {
		return nil
	}
	if err := allowance.Covers(nanoUSD(float64(pages) * price)); err != nil {
		named := filename
		if named == "" {
			named = "the attached document"
		}
		return failure.New(
			failure.Billing,
			fmt.Sprintf("Reading %s would pass this account's spend budget.", named),
			false,
			failure.ProviderDetails{},
			err,
		)
	}
	return nil
}

// nanoUSD converts a catalog price into the integer unit every meter in this
// gateway counts.
func nanoUSD(usd float64) int64 {
	return int64(math.Round(usd * 1e9))
}

// catalogPrices holds the prices a request needs before its route exists. Every
// question below is asked ahead of the planner, where no exact offering is
// known yet, so each answers with a floor rather than a bill. The callers name
// the questions rather than the whole snapshot, because a price is all they
// need.
type catalogPrices interface {
	// PagePriceFor is what one recognition model charges for one page.
	PagePriceFor(modelID string, operation starmapcatalogs.ProviderOperation) (float64, bool)
	// LowestPagePrice is the least any offering charges for one page.
	LowestPagePrice(operation starmapcatalogs.ProviderOperation) (float64, bool)
	// LowestSearchUnitPrice is the least one query against this model can
	// cost, on the offerings that bill a search unit.
	LowestSearchUnitPrice(modelID string) (float64, bool)
}

// catalogPrices answers what a unit costs on this request.
//
// The catalog the request already carries answers it, which is every
// deployment. A gateway assembled with prices of its own uses those instead,
// which is how a caller with no runtime lease reaches a price at all.
func (p *proxy) catalogPrices(ctx context.Context) catalogPrices {
	if p.prices != nil {
		return p.prices
	}
	if snapshot := catalogSnapshot(ctx); snapshot != nil {
		return snapshot
	}
	return nil
}

// catalogSnapshot reads the catalog already in force on this request. Like
// catalogGeneration below it never acquires a runtime of its own.
func catalogSnapshot(ctx context.Context) *runtimecatalog.RoutableSnapshot {
	lease := connectors.RuntimeLeaseFromContext(ctx)
	if lease == nil {
		return nil
	}
	return lease.Snapshot()
}

// catalogGeneration reads the catalog identity already in force on this
// request. It never acquires a runtime of its own: a lookup that had to take a
// lease would make an unreachable catalog a slower document read rather than
// an uncached one, and an empty generation is a complete answer here, because
// a key without one never reads and never writes.
func (p *proxy) catalogGeneration(ctx context.Context) string {
	snapshot := catalogSnapshot(ctx)
	if snapshot == nil {
		return ""
	}
	return snapshot.GenerationID()
}

// recognize sends the whole document to a catalogued recognition model and
// returns what it read.
//
// The whole document goes, not the pages that carry no text. Splitting a
// container into single-page documents needs a writer this gateway does not
// carry, and a recognizer that saw only the scanned pages would return them
// out of the document's own order.
func (p *proxy) recognize(
	ctx context.Context,
	req *ChatCompletionRequest,
	attached inference.Document,
	data []byte,
	mediaType string,
	extraction document.Extraction,
	policy *router.APIKeyConfig,
) (text, offering string, err error) {
	// No model is named. The catalog states which offerings serve this
	// operation and what a page costs at each of them, so the planner picks
	// one under the same cost and latency policy every other route uses. A
	// model named here would be a second engine table beside the catalog's.
	answer, err := p.router.RouteDocumentRecognition(ctx, &router.RecognitionRequest{
		Request: inference.RecognitionRequest{
			Document: inference.UploadedFile{
				Filename:  attached.Filename,
				MediaType: mediaType,
				Bytes:     data,
			},
			Pages: extraction.PageCount(),
		},
		APIKeyConfig: policy,
		TenantID:     req.TenantID,
	})
	if err != nil {
		return "", "", recognitionFailure(attached.Filename, err)
	}

	// A short answer is the failure this step actually has. A model that
	// stopped early returns the pages it reached, and passing those on would
	// hand the chat model a document that ends in the middle with nothing
	// saying so. The turn fails instead.
	if read := len(answer.Response.Pages); read != extraction.PageCount() {
		return "", "", recognitionFailure(attached.Filename, fmt.Errorf(
			"%w: read %d of %d pages at %s",
			document.ErrRecognitionFailed, read, extraction.PageCount(), answer.ModelUsed,
		))
	}
	return answer.Response.Text(), answer.ModelUsed, nil
}

// recognitionFailure names the recognition step in an answer the caller reads.
//
// It is one envelope for every way the step fails, because the caller's
// position is the same in all of them: the document was attached, the engine
// was named, and the gateway did not read it. The cause carries which of them
// happened to the operator.
func recognitionFailure(filename string, cause error) error {
	named := filename
	if named == "" {
		named = "the attached document"
	}
	return failure.New(
		failure.ProviderUnavailable,
		fmt.Sprintf("The recognition engine did not read %s.", named),
		false,
		failure.ProviderDetails{},
		fmt.Errorf("%w: %w", document.ErrRecognitionFailed, cause),
	)
}

// needsRecognition reports whether any page of the document carries no text
// layer.
//
// One scanned page is enough. A document whose other pages read cleanly would
// otherwise reach the model with that page silently blank, which is the same
// partial answer a truncated recognition would produce.
func needsRecognition(extraction document.Extraction) bool {
	for _, page := range extraction.Pages {
		if page.Scanned {
			return true
		}
	}
	return false
}

// carriesDocument reports whether any part of the request attaches a document.
// A request that attaches none pays nothing for this seam.
func carriesDocument(messages []inference.Message) bool {
	for _, message := range messages {
		for _, part := range message.Content {
			if part.Kind == inference.ContentDocument && part.Document != nil {
				return true
			}
		}
	}
	return false
}

// extractor returns the native engine, built with this gateway's bounds.
func (p *proxy) extractor() *document.Extractor {
	if p.documents != nil {
		return p.documents
	}
	return defaultExtractor
}

// defaultExtractor is the native engine a proxy built without configured
// bounds uses. It holds bounds and nothing else, so one value serves every
// request.
var defaultExtractor = document.NewExtractor(document.Limits{})

// renderDocument wraps extracted text in a boundary the model can read. A
// caller that attached three files gets three named blocks rather than one run
// of text with no seam between the documents.
func renderDocument(filename, text string) string {
	var builder strings.Builder
	builder.WriteString("<document")
	if filename != "" {
		fmt.Fprintf(&builder, " filename=%q", filename)
	}
	builder.WriteString(">\n")
	builder.WriteString(text)
	builder.WriteString("\n</document>")
	return builder.String()
}

// documentBytes reads the bytes behind one attached document.
//
// A stored reference is already resolved by the time this runs, and a remote
// URL is not fetched: reaching out to an address a request named would make
// the gateway a fetcher for whatever a caller points it at.
func documentBytes(attached inference.Document) ([]byte, string, error) {
	if len(attached.Data) > 0 {
		return attached.Data, attached.Format, nil
	}
	mediaType, encoded, isData := parseDocumentDataURL(attached.URL)
	if !isData {
		return nil, "", fmt.Errorf(
			"a parser plugin reads an attached document, and this one names a remote reference")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", fmt.Errorf("attached document is not valid base64")
	}
	if attached.Format != "" {
		mediaType = attached.Format
	}
	return data, mediaType, nil
}

// parseDocumentDataURL splits a base64 data URL into its media type and its
// payload. A URL in any other shape is not inline bytes.
func parseDocumentDataURL(url string) (mediaType, payload string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(url, prefix) {
		return "", "", false
	}
	separator := strings.IndexByte(url, ',')
	if separator < 0 {
		return "", "", false
	}
	header := url[len(prefix):separator]
	if !strings.HasSuffix(header, ";base64") {
		return "", "", false
	}
	return strings.TrimSuffix(header, ";base64"), url[separator+1:], true
}
