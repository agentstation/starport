package proxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/document"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	routepkg "github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/routing"
)

// PLG-V09 to PLG-V11. These tests hold the parser plugin's three promises: a
// scanned document reaches a recognition offering and its text reaches the
// chat model, the plugin never moves the chat route, and a page the recognizer
// did not return fails the turn instead of reaching the model as a gap.
//
// The fixtures are the native engine's own, read across the package boundary
// rather than copied. A PDF's cross reference table records byte offsets, so a
// second copy is a second thing that can drift, and the digest guard in
// internal/document covers only the originals.

func parserFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "document", "testdata", name))
	require.NoError(t, err)
	return data
}

// recognizingRouter answers the recognition route with pages a test names, and
// records what it was asked for. Everything else it inherits, so a chat route
// taken through this router is the same chat route every other test takes.
type recognizingRouter struct {
	*capturingRouter
	asked *routepkg.RecognitionRequest
	calls int
	pages []inference.RecognizedPage
	fail  error
}

func newRecognizingRouter(pages ...string) *recognizingRouter {
	recognized := make([]inference.RecognizedPage, len(pages))
	for index, text := range pages {
		recognized[index] = inference.RecognizedPage{Number: index + 1, Text: text}
	}
	return &recognizingRouter{capturingRouter: &capturingRouter{}, pages: recognized}
}

func (r *recognizingRouter) RouteDocumentRecognition(
	_ context.Context,
	req *routepkg.RecognitionRequest,
) (*routepkg.RecognitionResponse, error) {
	r.asked = req
	r.calls++
	if r.fail != nil {
		return nil, r.fail
	}
	return &routepkg.RecognitionResponse{
		Response:  inference.RecognitionResponse{Pages: r.pages},
		ModelUsed: "google/gemini-2.5-flash",
	}, nil
}

// parsedRequest builds a chat request carrying one inline document and the
// plugin that names an engine for it.
func parsedRequest(t *testing.T, fixture string, engine inference.ParserEngine) *ChatCompletionRequest {
	t.Helper()
	request := &ChatCompletionRequest{
		AccountID: "account-a",
		Request: inference.ChatRequest{
			Model: "openai/gpt-4o",
			Messages: []inference.Message{{
				Role: inference.RoleUser,
				Content: []inference.ContentPart{
					{Kind: inference.ContentText, Text: "summarize this"},
					{Kind: inference.ContentDocument, Document: &inference.Document{
						Filename: fixture,
						Format:   "application/pdf",
						URL: documentDataURL(StoredDocument{
							Filename: fixture,
							Data:     parserFixture(t, fixture),
						}),
					}},
				},
			}},
		},
	}
	request.Request.DocumentParser = inference.DocumentParser{Engine: engine}
	return request
}

// sentText returns the text the provider request carried, joined across parts.
func sentText(t *testing.T, req *routepkg.Request) string {
	t.Helper()
	require.NotNil(t, req)
	require.Len(t, req.ChatRequest.Messages, 1)
	parts, err := connectors.ParseMessageContent(req.ChatRequest.Messages[0].Content)
	require.NoError(t, err)
	var joined string
	for _, part := range parts {
		joined += part.Text
	}
	return joined
}

// recognitionCause returns the operator-facing cause behind a recognition
// refusal. The safe message names the document alone, because a caller has no
// use for which provider was tried.
func recognitionCause(t *testing.T, err error) string {
	t.Helper()
	var normalized *failure.Failure
	require.ErrorAs(t, err, &normalized)
	require.Equal(t, failure.ProviderUnavailable, normalized.Kind())
	require.NotNil(t, normalized.Unwrap())
	return normalized.Unwrap().Error()
}

// TestAScannedDocumentReachesRecognitionThenTheChatModel holds PLG-V09.
//
// Both halves matter. A gateway that recognized the page and sent the model
// the original bytes would have paid twice for one read, and a gateway that
// sent the model the recognized text without ordering the read would have sent
// it nothing at all.
func TestAScannedDocumentReachesRecognitionThenTheChatModel(t *testing.T) {
	t.Parallel()
	router := newRecognizingRouter("INVOICE 4471\nAmount due: $912.00")
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)

	require.NotNil(t, router.asked, "the scanned document never reached a recognition offering")
	require.Equal(t, "scanned.pdf", router.asked.Request.Document.Filename)
	require.Equal(t, 1, router.asked.Request.Pages,
		"the recognizer was not told how many pages it must return")
	require.Empty(t, router.asked.Request.Model,
		"a named model here would be an engine table beside the catalog's")
	require.Equal(t, "account-a", router.asked.AccountID,
		"the recognition read was charged to no account")

	sent := sentText(t, router.req)
	require.Contains(t, sent, "INVOICE 4471")
	require.Contains(t, sent, "summarize this")
}

// TestARoutableDocumentIsReadNatively states where the boundary between the
// two engines sits. A page that carries a text layer is read in this process
// for nothing, even when the caller named the paid engine, because the paid
// engine would return the same text and charge for it.
func TestARoutableDocumentIsReadNatively(t *testing.T) {
	t.Parallel()
	router := newRecognizingRouter("this text was never asked for")
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "handwritten.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)

	require.Nil(t, router.asked, "a document with a text layer was sent to a provider to be read")
	require.Contains(t, sentText(t, router.req), "Starport native extraction proof")
}

// TestTheNativeEngineNeverReachesAProvider holds invariant P3 at the seam that
// could break it. The native engine is a leaf package, so nothing inside it
// can call out; this states that the code around it does not call out either.
func TestTheNativeEngineNeverReachesAProvider(t *testing.T) {
	t.Parallel()
	router := newRecognizingRouter("unused")
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineNative))
	require.NoError(t, err)
	require.Nil(t, router.asked,
		"the native engine ordered a provider read")
}

// TestThePluginDoesNotMoveTheChatRoute holds PLG-V10 at the proxy.
//
// The route is planned from what the caller sent, and the parser rewrites what
// the provider receives. Reversing that order would make the plugin a routing
// control: a document turned into text stops looking like a document, and the
// planner would then offer the request models that read text alone.
func TestThePluginDoesNotMoveTheChatRoute(t *testing.T) {
	t.Parallel()
	withPlugin := newRecognizingRouter("recognized page")
	plugged := &proxy{router: withPlugin}
	_, err := plugged.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)

	plain := parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition)
	plain.Request.DocumentParser = inference.DocumentParser{}
	withoutPlugin := newRecognizingRouter()
	unplugged := &proxy{router: withoutPlugin}
	_, err = unplugged.ProcessChatCompletion(context.Background(), plain)
	require.NoError(t, err)

	require.Equal(t, withoutPlugin.req.Models, withPlugin.req.Models)
	require.Equal(t, withoutPlugin.req.ChatRequest.Model, withPlugin.req.ChatRequest.Model)
	require.Equal(t, withoutPlugin.req.Metadata, withPlugin.req.Metadata,
		"the plugin changed what the planner was told the request holds")
	require.Equal(t, []string{"document"}, withPlugin.req.Metadata.RequiredModalities,
		"a parsed request no longer asked for a model that reads documents")
}

// TestAShortRecognitionFailsTheWholeTurn holds PLG-V11 and decision PLG-D6.
//
// A recognizer that stopped early returns the pages it reached and no error.
// Sending those on would hand the model a document that ends in the middle
// with nothing saying so, and the model would answer confidently from half a
// contract. The page count the native read produced is what makes the gap
// visible, and the turn fails on it.
func TestAShortRecognitionFailsTheWholeTurn(t *testing.T) {
	t.Parallel()
	router := newRecognizingRouter() // the recognizer returned no pages at all
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))

	require.ErrorIs(t, err, document.ErrRecognitionFailed)
	require.Nil(t, router.req, "partial text reached the chat model")

	var normalized *failure.Failure
	require.ErrorAs(t, err, &normalized)
	require.Contains(t, normalized.SafeMessage(), "scanned.pdf",
		"the caller cannot tell which of its attachments failed")
	require.Contains(t, recognitionCause(t, err), "read 0 of 1 pages")
}

// TestARecognitionRouteFailureNamesTheStep holds the other half of PLG-V11. A
// provider failure inside the recognition read must not surface as a chat
// routing failure, because the model the caller named was never the model that
// failed.
func TestARecognitionRouteFailureNamesTheStep(t *testing.T) {
	t.Parallel()
	router := newRecognizingRouter("unreachable")
	router.fail = errors.New("no provider serves document recognition")
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))

	require.ErrorIs(t, err, document.ErrRecognitionFailed)
	require.Contains(t, recognitionCause(t, err), "no provider serves document recognition")

	var routingErr *RoutingError
	require.NotErrorAs(t, err, &routingErr,
		"a failed document read was reported as a failed chat route")
	require.Nil(t, router.req)
}

// TestAMalformedDocumentIsTheCallersMistake separates the two refusals. Bytes
// that are not the document they claim to be is something the caller can fix,
// and a 503 would tell it to retry an upload that never parses.
func TestAMalformedDocumentIsTheCallersMistake(t *testing.T) {
	t.Parallel()
	router := newRecognizingRouter("unused")
	service := &proxy{router: router}

	request := parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition)
	request.Request.Messages[0].Content[1].Document.URL = documentDataURL(
		StoredDocument{Filename: "scanned.pdf", Data: []byte("%PDF-1.7 and then nothing")})

	_, err := service.ProcessChatCompletion(context.Background(), request)
	var invalid *ValidationError
	require.ErrorAs(t, err, &invalid)
	require.NotErrorIs(t, err, document.ErrRecognitionFailed)
	require.Nil(t, router.asked, "malformed bytes were sent to a provider to be read")
}

// TestAnUnnamedEngineLeavesTheDocumentAlone states the default. A caller that
// attached a document and asked for no engine wants the model to read the file
// itself, and rewriting it would take that choice away.
func TestAnUnnamedEngineLeavesTheDocumentAlone(t *testing.T) {
	t.Parallel()
	router := newRecognizingRouter("never read")
	service := &proxy{router: router}

	request := parsedRequest(t, "handwritten.pdf", inference.ParserEngineRecognition)
	request.Request.DocumentParser = inference.DocumentParser{}

	_, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	require.Nil(t, router.asked)
	require.NotContains(t, sentText(t, router.req), "Starport native extraction proof",
		"a request that named no engine was parsed anyway")
}

// TestAnUnknownEngineIsRefusedRatherThanIgnored holds decision PLG-D4. A
// caller that asked for an engine this gateway does not have must hear so. The
// silent alternative is worse than a refusal: the model answers from a
// document nobody read the way the caller asked.
func TestAnUnknownEngineIsRefusedRatherThanIgnored(t *testing.T) {
	t.Parallel()
	router := newRecognizingRouter("unused")
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "handwritten.pdf", inference.ParserEngine("mistral-ocr")))

	var invalid *ValidationError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, "plugins", invalid.Field)
	require.Nil(t, router.req)
}

// TestParsingLeavesTheCallersRequestAlone states that the rewritten request
// stays inside the proxy, the way stored-file resolution does. A middleware
// above the proxy still holds the request the caller sent, and usage reporting
// reads it after the call returns.
func TestParsingLeavesTheCallersRequestAlone(t *testing.T) {
	t.Parallel()
	router := newRecognizingRouter("recognized")
	service := &proxy{router: router}

	request := parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition)
	_, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)

	part := request.Request.Messages[0].Content[1]
	require.Equal(t, inference.ContentDocument, part.Kind)
	require.NotNil(t, part.Document)
}

// TestRecognitionIsAServedOperation states the catalog side of PLG-V09. The
// route above can only be planned if the gateway serves the operation, and the
// planner refuses a request naming any operation outside that set.
func TestRecognitionIsAServedOperation(t *testing.T) {
	t.Parallel()
	require.True(t, routing.ServedOperations().Contains(routing.OperationDocumentsRecognition))
}
