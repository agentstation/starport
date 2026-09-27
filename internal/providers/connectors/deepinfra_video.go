package connectors

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// deepInfraVideoConnector implements the native duration-based video protocol.
type deepInfraVideoConnector struct {
	providerID catalogs.ProviderID
	client     *http.Client
}

func newDeepInfraVideoConnector(id catalogs.ProviderID, config ProviderConfig) (Connector, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &deepInfraVideoConnector{providerID: id, client: newProviderHTTPClient(config)}, nil
}

func (c *deepInfraVideoConnector) Name() string { return string(c.providerID) }
func (c *deepInfraVideoConnector) Close() error { c.client.CloseIdleConnections(); return nil }
func (c *deepInfraVideoConnector) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	return nil, ErrTransportOperationUnsupported
}
func (c *deepInfraVideoConnector) ChatStream(context.Context, *ChatRequest) (ChatStream, error) {
	return nil, ErrTransportOperationUnsupported
}
func (c *deepInfraVideoConnector) Embeddings(context.Context, *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	return nil, ErrTransportOperationUnsupported
}

func (c *deepInfraVideoConnector) GenerateVideo(ctx context.Context, request *NativeVideoRequest) (*NativeVideoResponse, error) {
	if request == nil || strings.TrimSpace(request.Prompt) == "" || request.Seconds <= 0 || request.MaxBytes <= 0 || request.MaxBytes > (math.MaxInt64-65536)/2 {
		return nil, fmt.Errorf("%w: native video requires resolved inputs and an asset bound", ErrInvalidMediaRequest)
	}
	resolution, orientation, err := deepInfraVideoSize(request.Size)
	if err != nil {
		return nil, err
	}
	endpoint, err := selectedEndpoint(request.Endpoint, catalogs.EndpointTypeDeepInfraVideo)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Prompt         string `json:"prompt"`
		NegativePrompt string `json:"negative_prompt,omitempty"`
		Seconds        int64  `json:"seconds"`
		Resolution     string `json:"resolution"`
		Orientation    string `json:"orientation"`
		Seed           *int64 `json:"seed,omitempty"`
	}{request.Prompt, request.NegativePrompt, request.Seconds, resolution, orientation, request.Seed}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
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
		return nil, &APIError{StatusCode: response.StatusCode, Provider: string(c.providerID), Message: "native video inference failed"}
	}
	return decodeDeepInfraVideo(response.Body, request.MaxBytes)
}

func deepInfraVideoSize(size string) (string, string, error) {
	width, height, found := strings.Cut(size, "x")
	w, we := strconv.ParseInt(width, 10, 32)
	h, he := strconv.ParseInt(height, 10, 32)
	if !found || we != nil || he != nil || w <= 0 || h <= 0 || strconv.FormatInt(w, 10)+"x"+strconv.FormatInt(h, 10) != size {
		return "", "", ErrInvalidMediaRequest
	}
	// This protocol expresses a 16:9 frame through resolution and orientation.
	if w*9 == h*16 {
		return strconv.FormatInt(h, 10) + "p", "landscape", nil
	}
	if h*9 == w*16 {
		return strconv.FormatInt(w, 10) + "p", "portrait", nil
	}
	return "", "", ErrInvalidMediaRequest
}

func decodeDeepInfraVideo(reader io.Reader, maxBytes int64) (*NativeVideoResponse, error) {
	if maxBytes <= 0 || maxBytes > (math.MaxInt64-65536)/2 {
		return nil, ErrInvalidMediaRequest
	}
	// Base64 overhead and bounded metadata cannot grow the response without limit.
	bound := maxBytes*2 + 65536
	data, err := io.ReadAll(io.LimitReader(reader, bound+1))
	if err != nil || int64(len(data)) > bound {
		return nil, fmt.Errorf("%w: native video response exceeds its bound or is incomplete", ErrInvalidMediaRequest)
	}
	var payload struct {
		RequestID string `json:"request_id"`
		VideoURL  string `json:"video_url"`
		Status    *struct {
			State         *string  `json:"status"`
			OutputSeconds *int64   `json:"output_length"`
			Cost          *float64 `json:"cost"`
		} `json:"inference_status"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		// Decoder errors can contain provider content. Expose only the failure class.
		return nil, fmt.Errorf("%w: invalid native video response", ErrInvalidMediaRequest)
	}
	result := &NativeVideoResponse{RequestID: payload.RequestID}
	if payload.Status != nil {
		if payload.Status.OutputSeconds != nil && *payload.Status.OutputSeconds >= 0 {
			result.OutputSeconds = payload.Status.OutputSeconds
		}
		if payload.Status.Cost != nil && *payload.Status.Cost >= 0 {
			result.EstimatedUSD = payload.Status.Cost
		}
		// The schema defaults an omitted status to succeeded. An explicit value must agree.
		if payload.Status.State != nil && *payload.Status.State != "succeeded" {
			return result, fmt.Errorf("%w: native video completion is unconfirmed", ErrInvalidMediaRequest)
		}
	}
	if strings.TrimSpace(result.RequestID) == "" {
		return result, fmt.Errorf("%w: native video response has no request ID", ErrInvalidMediaRequest)
	}
	if strings.HasPrefix(payload.VideoURL, "data:") {
		prefix, content, found := strings.Cut(payload.VideoURL, ",")
		if !found || prefix != "data:video/mp4;base64" {
			return result, fmt.Errorf("%w: unsupported native video data URL", ErrInvalidMediaRequest)
		}
		asset, err := base64.StdEncoding.Strict().DecodeString(content)
		if err != nil || len(asset) == 0 || int64(len(asset)) > maxBytes {
			return result, fmt.Errorf("%w: native video asset is invalid or exceeds its bound", ErrInvalidMediaRequest)
		}
		result.Asset = JobAsset{ContentType: "video/mp4", Bytes: asset}
		return result, nil
	}
	asset, err := url.Parse(payload.VideoURL)
	if err != nil || asset.User != nil || asset.Fragment != "" || asset.Opaque != "" ||
		!(asset.Scheme == "https" && asset.Host != "" || asset.Scheme == "" && asset.Host == "" && strings.HasPrefix(asset.Path, "/")) {
		return result, fmt.Errorf("%w: invalid native video asset reference", ErrInvalidMediaRequest)
	}
	// Parsing a provider URL grants no network access and forwards no credential.
	result.AssetURL = payload.VideoURL
	return result, nil
}
