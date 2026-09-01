package controllers

import (
	"net/http"
	"strings"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/proxy"
)

// RerankController serves POST /v1/rerank and POST /api/v1/rerank. The two
// paths plan one route and reach one provider. They differ only at the edge,
// where each protocol owns its own wire names, so the handler below reads the
// decoding its protocol produced and writes the answer that protocol states.
type RerankController struct {
	*BaseHandler
}

// NewRerankController creates an OpenAI-protocol rerank controller.
func NewRerankController(service proxy.Proxy) *RerankController {
	return &RerankController{BaseHandler: NewProtocolBaseHandler(service, ProtocolOpenAI)}
}

// NewOpenRouterRerankController creates an OpenRouter-protocol rerank
// controller.
func NewOpenRouterRerankController(service proxy.Proxy) *RerankController {
	return &RerankController{BaseHandler: NewProtocolBaseHandler(service, ProtocolOpenRouter)}
}

// rerankDecoding is what each protocol's decoder produced. The canonical
// request is common to both. The two remaining members are the parts only one
// protocol has, and each is empty on the other.
type rerankDecoding struct {
	request inference.RerankRequest
	// returnDocuments is the /v1 flag that asks for the ranked text echoed.
	// The OpenRouter schema requires the echo on every result and has no flag.
	returnDocuments bool
	// unenforced names documented provider fields the OpenRouter request used
	// that this gateway accepts without acting on.
	unenforced []string
}

// Create handles POST /v1/rerank and POST /api/v1/rerank.
func (h *RerankController) Create(w http.ResponseWriter, r *http.Request) {
	decoding, err := h.decodeRerank(r)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}
	if len(decoding.unenforced) > 0 {
		// Documented provider fields Starport accepts but cannot yet enforce
		// are reported loudly instead of silently dropped.
		w.Header().Set("X-Starport-Unenforced-Provider-Fields", strings.Join(decoding.unenforced, ","))
	}

	ctx := r.Context()
	req := &proxy.RerankRequest{Request: decoding.request}
	req.APIKey = h.getAPIKey(ctx)
	req.AccountID = h.getAccountID(ctx)
	req.KeyID = h.getAPIKeyID(ctx)
	req.TeamID = h.getTeamID(ctx)
	req.APIKeyConfig, err = h.getAPIKeyRoutingConfig(ctx)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	req.RequestID = h.getRequestID(ctx)
	req.Protocol = string(h.protocol)

	resp, err := h.service.ProcessRerank(ctx, req)
	if err != nil {
		h.logError(ctx, err, "rerank failed")
		h.writeError(w, err)
		return
	}
	if err := h.writeRerank(w, resp, decoding); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}

func (h *RerankController) decodeRerank(r *http.Request) (rerankDecoding, error) {
	if h.protocol == ProtocolOpenRouter {
		decoded, err := openrouter.DecodeRerank(r.Body)
		if err != nil {
			return rerankDecoding{}, err
		}
		return rerankDecoding{
			request:    decoded.Request,
			unenforced: decoded.UnenforcedProviderFields,
		}, nil
	}
	decoded, err := openai.DecodeRerank(r.Body)
	if err != nil {
		return rerankDecoding{}, err
	}
	return rerankDecoding{
		request:         decoded.Request,
		returnDocuments: decoded.ReturnDocuments,
	}, nil
}

// writeRerank publishes one answer. Both encoders can refuse it: a result that
// names a document the request never held, or a score outside the unit
// interval, produces output that reads as correct. That refusal is a gateway
// failure rather than a body fault, so it reaches the caller through the same
// error path a failed route reaches it through.
func (h *RerankController) writeRerank(
	w http.ResponseWriter,
	resp *proxy.RerankResponse,
	decoding rerankDecoding,
) error {
	if h.protocol == ProtocolOpenRouter {
		encoded, err := openrouter.EncodeRerank(resp.Response, decoding.request, resp.ProviderUsed, rerankCostUSD(resp))
		if err != nil {
			h.writeError(w, err)
			return err
		}
		return openrouter.WriteJSON(w, http.StatusOK, encoded)
	}
	encoded, err := openai.EncodeRerank(resp.Response, openai.RerankDecoding{
		Request:         decoding.request,
		ReturnDocuments: decoding.returnDocuments,
	})
	if err != nil {
		h.writeError(w, err)
		return err
	}
	return openai.WriteJSON(w, http.StatusOK, encoded)
}

// rerankCostUSD states the gateway's own accounting in the unit the OpenRouter
// usage block names. A turn nothing priced reports no cost rather than a zero
// one, because a caller reading zero would take it for a free request.
func rerankCostUSD(resp *proxy.RerankResponse) *float64 {
	if resp.Cost == nil {
		return nil
	}
	usd := float64(resp.Cost.NanoUSD) / 1e9
	return &usd
}
