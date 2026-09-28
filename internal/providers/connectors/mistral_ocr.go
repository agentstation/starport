package connectors

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// mistralOCRConnector implements standard synchronous OCR without annotation charges.
type mistralOCRConnector struct {
	providerID catalogs.ProviderID
	client     *http.Client
}

func newMistralOCRConnector(id catalogs.ProviderID, config ProviderConfig) (Connector, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &mistralOCRConnector{providerID: id, client: newProviderHTTPClient(config)}, nil
}

func (c *mistralOCRConnector) Name() string { return string(c.providerID) }
func (c *mistralOCRConnector) Close() error { c.client.CloseIdleConnections(); return nil }
func (c *mistralOCRConnector) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	return nil, ErrTransportOperationUnsupported
}
func (c *mistralOCRConnector) ChatStream(context.Context, *ChatRequest) (ChatStream, error) {
	return nil, ErrTransportOperationUnsupported
}
func (c *mistralOCRConnector) Embeddings(context.Context, *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	return nil, ErrTransportOperationUnsupported
}

// RecognizeDocument sends only the counted PDF pages to the selected OCR endpoint.
func (c *mistralOCRConnector) RecognizeDocument(ctx context.Context, request *RecognitionRequest) (*RecognitionResponse, error) {
	if request == nil || !request.Document.Present() || request.Document.MediaType != recognitionMediaType || request.Pages <= 0 || request.MaxTokens != nil {
		return nil, fmt.Errorf("%w: OCR requires counted PDF pages without a token cap", ErrInvalidMediaRequest)
	}
	endpoint, err := selectedEndpoint(request.Endpoint, catalogs.EndpointTypeMistralOCR)
	if err != nil {
		return nil, err
	}
	// Explicit page selection enforces the reserved bound even if the provider counts more pages.
	pages := make([]int, request.Pages)
	for i := range pages {
		pages[i] = i
	}
	payload := struct {
		Model    string `json:"model"`
		Document struct {
			Type string `json:"type"`
			URL  string `json:"document_url"`
		} `json:"document"`
		Pages              []int `json:"pages"`
		IncludeImageBase64 bool  `json:"include_image_base64"`
		IncludeBlocks      bool  `json:"include_blocks"`
	}{Model: request.Model, Pages: pages}
	payload.Document.Type = "document_url"
	payload.Document.URL = recognitionDataURL(request.Document)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode OCR request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if err := applyRequestAuthentication(request.Credential, req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := doRequest(c.client, req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		// Provider bodies can contain document text. Keep errors free of document content.
		return nil, &APIError{StatusCode: response.StatusCode, Provider: string(c.providerID), Message: "document recognition failed"}
	}
	return decodeMistralOCR(response.Body, request.Pages)
}

func decodeMistralOCR(body io.Reader, pages int) (*RecognitionResponse, error) {
	var answer struct {
		Pages []struct {
			Index    *int    `json:"index"`
			Markdown *string `json:"markdown"`
		} `json:"pages"`
		Usage *struct {
			Pages *int `json:"pages_processed"`
		} `json:"usage_info"`
	}
	if err := json.UnmarshalRead(body, &answer); err != nil {
		return nil, fmt.Errorf("decode OCR response: %w", err)
	}
	result := &RecognitionResponse{}
	if answer.Usage != nil && answer.Usage.Pages != nil && *answer.Usage.Pages >= 0 {
		result.ProcessedPages = answer.Usage.Pages
	}
	seen := make(map[int]bool, len(answer.Pages))
	for _, page := range answer.Pages {
		if page.Markdown == nil || page.Index == nil || *page.Index < 0 || *page.Index >= pages || seen[*page.Index] {
			// Retain raw billing evidence even when the page text cannot be used.
			return result, fmt.Errorf("%w: OCR page text or index is missing, repeated, or outside the document", ErrInvalidMediaRequest)
		}
		seen[*page.Index] = true
		result.Pages = append(result.Pages, RecognizedPage{Number: *page.Index + 1, Text: *page.Markdown})
	}
	slices.SortFunc(result.Pages, func(a, b RecognizedPage) int { return a.Number - b.Number })
	return result, nil
}
