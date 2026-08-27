package proxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/agentstation/starport/internal/document"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
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
) (*ChatCompletionRequest, error) {
	engine := req.Request.DocumentParser.Engine
	if !req.Request.DocumentParser.Requested() || !carriesDocument(req.Request.Messages) {
		return req, nil
	}
	if !engine.Known() {
		// A codec refuses an unknown engine before the request reaches here.
		// This arm is what keeps that true of a caller that reaches the proxy
		// through another protocol later.
		return nil, &ValidationError{
			Field:   "plugins",
			Message: fmt.Sprintf("unknown parser engine %q", engine),
		}
	}

	parsed := *req
	parsed.Request = req.Request.Clone()
	for messageIndex := range parsed.Request.Messages {
		content := parsed.Request.Messages[messageIndex].Content
		for partIndex := range content {
			part := &content[partIndex]
			if part.Kind != inference.ContentDocument || part.Document == nil {
				continue
			}
			text, err := p.readDocument(ctx, &parsed, *part.Document, engine, policy)
			if err != nil {
				return nil, err
			}
			*part = inference.ContentPart{
				Kind: inference.ContentText,
				Text: renderDocument(part.Document.Filename, text),
			}
		}
	}
	return &parsed, nil
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
) (string, error) {
	data, mediaType, err := documentBytes(attached)
	if err != nil {
		return "", &ValidationError{Field: fieldMessages, Message: err.Error()}
	}

	extraction, err := p.extractor().Extract(ctx, document.Input{
		Data:     data,
		Format:   mediaType,
		Filename: attached.Filename,
	})
	if err != nil {
		// Every refusal the native engine raises is a statement about the
		// bytes the caller attached, so it reaches the caller as one.
		return "", &ValidationError{Field: fieldMessages, Message: err.Error()}
	}

	if engine != inference.ParserEngineRecognition || !needsRecognition(extraction) {
		return extraction.Text, nil
	}
	return p.recognize(ctx, req, attached, data, mediaType, extraction, policy)
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
) (string, error) {
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
		return "", recognitionFailure(attached.Filename, err)
	}

	// A short answer is the failure this step actually has. A model that
	// stopped early returns the pages it reached, and passing those on would
	// hand the chat model a document that ends in the middle with nothing
	// saying so. The turn fails instead.
	if read := len(answer.Response.Pages); read != extraction.PageCount() {
		return "", recognitionFailure(attached.Filename, fmt.Errorf(
			"%w: read %d of %d pages at %s",
			document.ErrRecognitionFailed, read, extraction.PageCount(), answer.ModelUsed,
		))
	}
	return answer.Response.Text(), nil
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
