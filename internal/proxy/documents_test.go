package proxy

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	routepkg "github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/routing"
)

// countingResolver answers for one account and counts every read. The count is
// the point: the gateway promises one read for each request, and only a
// counter separates that from one read for each attempt.
type countingResolver struct {
	tenant   string
	files    map[string]StoredDocument
	reads    int
	failWith error
}

func (r *countingResolver) ResolveDocument(
	_ context.Context,
	tenant, id string,
) (StoredDocument, bool, error) {
	r.reads++
	if r.failWith != nil {
		return StoredDocument{}, false, r.failWith
	}
	if tenant != r.tenant {
		return StoredDocument{}, false, nil
	}
	stored, ok := r.files[id]
	return stored, ok, nil
}

// documentRequest builds a chat request whose single part names one document.
// A nil fileID sends the bytes inline instead, which is the shape the stored
// path has to reproduce exactly.
func documentRequest(tenant, fileID, filename string, inline []byte) *ChatCompletionRequest {
	document := &inference.Document{Filename: filename}
	if fileID != "" {
		document.FileID = fileID
	} else {
		document.URL = documentDataURL(StoredDocument{Filename: filename, Data: inline})
	}
	return &ChatCompletionRequest{
		TenantID: tenant,
		Request: inference.ChatRequest{
			Model: "openai/gpt-4o",
			Messages: []inference.Message{{
				Role: inference.RoleUser,
				Content: []inference.ContentPart{
					{Kind: inference.ContentText, Text: "summarize this"},
					{Kind: inference.ContentDocument, Document: document},
				},
			}},
		},
	}
}

// TestAStoredFileReachesTheProviderAsInlineBytesDo holds the first half of
// FIL-V18. A caller that stored a document once and a caller that pasted the
// same bytes into the request have asked the same question, so the provider
// must receive the same request. Anything less would make the answer depend on
// how the caller delivered the file.
func TestAStoredFileReachesTheProviderAsInlineBytesDo(t *testing.T) {
	t.Parallel()
	payload := []byte("%PDF-1.7 quarterly results")
	resolver := &countingResolver{
		tenant: "tenant-a",
		files:  map[string]StoredDocument{"file-1": {Filename: "report.pdf", Data: payload}},
	}

	storedRouter := &capturingRouter{}
	stored := &proxy{router: storedRouter, files: resolver}
	_, err := stored.ProcessChatCompletion(context.Background(),
		documentRequest("tenant-a", "file-1", "report.pdf", nil))
	require.NoError(t, err)

	inlineRouter := &capturingRouter{}
	inline := &proxy{router: inlineRouter}
	_, err = inline.ProcessChatCompletion(context.Background(),
		documentRequest("tenant-a", "", "report.pdf", payload))
	require.NoError(t, err)

	require.Equal(t, inlineRouter.req.ChatRequest, storedRouter.req.ChatRequest,
		"a stored document reached the provider as a different request")
}

// TestAnotherTenantsFileIsNotFoundBeforeAnyProviderCall holds the second half
// of FIL-V18.
//
// The refusal has to land before routing, not as a provider failure, because a
// request that reached a provider has already spent a credential and a
// caller's money on a file the caller may not read. The identifier exists;
// the answer must not say so.
func TestAnotherTenantsFileIsNotFoundBeforeAnyProviderCall(t *testing.T) {
	t.Parallel()
	resolver := &countingResolver{
		tenant: "tenant-a",
		files:  map[string]StoredDocument{"file-1": {Filename: "report.pdf", Data: []byte("private")}},
	}
	router := &capturingRouter{}
	service := &proxy{router: router, files: resolver}

	_, err := service.ProcessChatCompletion(context.Background(),
		documentRequest("tenant-b", "file-1", "report.pdf", nil))
	require.ErrorIs(t, err, ErrStoredFileNotFound)
	require.Nil(t, router.req, "the refused request reached the router")

	// An identifier nobody holds reads exactly the same way, so the refusal
	// tells a caller nothing about which identifiers exist.
	_, unknownErr := service.ProcessChatCompletion(context.Background(),
		documentRequest("tenant-a", "file-404", "report.pdf", nil))
	require.ErrorIs(t, unknownErr, ErrStoredFileNotFound)
}

// retryingRouter runs the attempt hook several times for one request, the way
// a real route does when the first provider fails.
type retryingRouter struct {
	unroutedMedia
	attempts int
}

func (r *retryingRouter) SelectModel(context.Context, *routepkg.Request) (string, connectors.Connector, error) {
	return "", nil, nil
}

func (r *retryingRouter) RouteWithFallback(_ context.Context, req *routepkg.Request) (*routepkg.Response, error) {
	attempt := *req.ChatRequest
	for range 3 {
		r.attempts++
		if req.PrepareAttempt != nil {
			if prepared := req.PrepareAttempt(routing.Route{ProviderModelID: attempt.Model}, &attempt); prepared != nil {
				attempt = *prepared
			}
		}
	}
	return &routepkg.Response{
		ChatResponse: &connectors.ChatResponse{
			ID: "chatcmpl-test", Object: "chat.completion", Created: 1,
			Model: attempt.Model,
			Choices: []connectors.Choice{
				{Index: 0, Message: connectors.Message{Role: "assistant", Content: "ok"}},
			},
		},
		ModelUsed: attempt.Model,
	}, nil
}

func (r *retryingRouter) RouteStream(context.Context, *routepkg.Request) (execution.ManagedStream, error) {
	return nil, nil
}

func (r *retryingRouter) RouteEmbeddings(context.Context, *routepkg.EmbeddingRequest) (*routepkg.EmbeddingResponse, error) {
	return nil, nil
}

// TestARetryReadsTheStoredBytesOnce states why resolution sits ahead of the
// attempt loop rather than inside it.
//
// Reading for each attempt would charge the byte store for every retry, and
// worse, a file that expired between two attempts would let one request send
// two different documents to two providers.
func TestARetryReadsTheStoredBytesOnce(t *testing.T) {
	t.Parallel()
	resolver := &countingResolver{
		tenant: "tenant-a",
		files:  map[string]StoredDocument{"file-1": {Filename: "report.pdf", Data: []byte("bytes")}},
	}
	router := &retryingRouter{}
	service := &proxy{router: router, files: resolver}

	_, err := service.ProcessChatCompletion(context.Background(),
		documentRequest("tenant-a", "file-1", "report.pdf", nil))
	require.NoError(t, err)
	require.Equal(t, 3, router.attempts, "the router did not retry")
	require.Equal(t, 1, resolver.reads)
}

// TestOneRequestNamingAFileTwiceReadsItOnce states the other read bound. A
// request may cite the same document in several parts, and each citation is
// the same bytes.
func TestOneRequestNamingAFileTwiceReadsItOnce(t *testing.T) {
	t.Parallel()
	resolver := &countingResolver{
		tenant: "tenant-a",
		files:  map[string]StoredDocument{"file-1": {Filename: "report.pdf", Data: []byte("bytes")}},
	}
	service := &proxy{router: &capturingRouter{}, files: resolver}

	request := documentRequest("tenant-a", "file-1", "report.pdf", nil)
	request.Request.Messages = append(request.Request.Messages, inference.Message{
		Role: inference.RoleUser,
		Content: []inference.ContentPart{{
			Kind: inference.ContentDocument, Document: &inference.Document{FileID: "file-1"},
		}},
	})

	_, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 1, resolver.reads)
}

// TestResolutionLeavesTheCallersRequestAlone states that the resolved bytes
// stay inside the proxy. The HTTP layer still holds the request the caller
// sent, and a middleware above the proxy may read it after the call returns.
func TestResolutionLeavesTheCallersRequestAlone(t *testing.T) {
	t.Parallel()
	resolver := &countingResolver{
		tenant: "tenant-a",
		files:  map[string]StoredDocument{"file-1": {Filename: "report.pdf", Data: []byte("bytes")}},
	}
	service := &proxy{router: &capturingRouter{}, files: resolver}

	request := documentRequest("tenant-a", "file-1", "report.pdf", nil)
	_, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)

	document := request.Request.Messages[0].Content[1].Document
	require.Equal(t, "file-1", document.FileID)
	require.Empty(t, document.URL)
}

// TestAnInlineRequestNeverReachesTheResolver states that the inline path is
// untouched by this seam. A text or inline-document request pays nothing for
// stored files: no clone, no map, and no resolver call.
func TestAnInlineRequestNeverReachesTheResolver(t *testing.T) {
	t.Parallel()
	resolver := &countingResolver{tenant: "tenant-a"}
	service := &proxy{router: &capturingRouter{}, files: resolver}

	_, err := service.ProcessChatCompletion(context.Background(),
		documentRequest("tenant-a", "", "report.pdf", []byte("inline")))
	require.NoError(t, err)
	require.Equal(t, 0, resolver.reads)
}

// TestAGatewayWithoutFileStorageRefusesAFileReference states the composition
// case. A deployment that stores no files must say so, rather than send the
// model a document part with nothing in it.
func TestAGatewayWithoutFileStorageRefusesAFileReference(t *testing.T) {
	t.Parallel()
	router := &capturingRouter{}
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(),
		documentRequest("tenant-a", "file-1", "report.pdf", nil))
	var invalid *ValidationError
	require.ErrorAs(t, err, &invalid)
	require.Nil(t, router.req)
}

// TestAnUnreachableFileStoreFailsTheRequest states that a byte store that went
// away is not a missing file. Reporting not found would tell the caller to
// stop retrying a request that a working store would answer.
func TestAnUnreachableFileStoreFailsTheRequest(t *testing.T) {
	t.Parallel()
	resolver := &countingResolver{tenant: "tenant-a", failWith: errors.New("store unreachable")}
	service := &proxy{router: &capturingRouter{}, files: resolver}

	_, err := service.ProcessChatCompletion(context.Background(),
		documentRequest("tenant-a", "file-1", "report.pdf", nil))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrStoredFileNotFound)
}

// TestARequestNamingTwoDocumentSourcesIsRefused states the canonical backstop.
// Each codec refuses the conflict at its own field path, and this check holds
// the same rule for a request that reached the proxy some other way.
func TestARequestNamingTwoDocumentSourcesIsRefused(t *testing.T) {
	t.Parallel()
	router := &capturingRouter{}
	service := &proxy{router: router}

	request := documentRequest("tenant-a", "", "report.pdf", []byte("inline"))
	request.Request.Messages[0].Content[1].Document.FileID = "file-1"

	_, err := service.ProcessChatCompletion(context.Background(), request)
	var invalid *ValidationError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, "messages[0].content[1].file", invalid.Field)
	require.Nil(t, router.req)
}

// TestTheStoredNameDecidesTheMediaType states how a document announces what it
// is. The record carries a filename and nothing else about the bytes, so the
// extension is the only evidence there is, and a name that carries none still
// produces a well-formed data URL.
func TestTheStoredNameDecidesTheMediaType(t *testing.T) {
	t.Parallel()
	require.Equal(t, "application/pdf", documentMediaType("report.pdf"))
	require.Equal(t, defaultDocumentMediaType, documentMediaType("report"))
	require.Equal(t, defaultDocumentMediaType, documentMediaType(""))
	require.NotContains(t, documentMediaType("notes.txt"), ";")
}
