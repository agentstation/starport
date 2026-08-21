package proxy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/presets"
)

// PresetReferencePrefix selects a stored preset through the model field,
// matching OpenRouter's "@preset/<name>" reference.
const PresetReferencePrefix = "@preset/"

// PresetSource resolves one stored preset by name. The concept-owned
// repository in internal/presets satisfies it.
type PresetSource interface {
	Get(ctx context.Context, name string) (presets.Record, error)
}

// PresetResolver is a proxy middleware that resolves preset references on
// chat requests and merges the stored configuration into them. Fields the
// request supplies win over preset fields; an unknown preset fails the
// request with ErrPresetNotFound before any routing happens.
type PresetResolver struct {
	source PresetSource
}

// NewPresetResolver creates the resolution middleware around one source.
func NewPresetResolver(source PresetSource) *PresetResolver {
	return &PresetResolver{source: source}
}

// Wrap implements Middleware.
func (r *PresetResolver) Wrap(next Proxy) Proxy {
	return &presetResolverService{Proxy: next, resolver: r}
}

type presetResolverService struct {
	Proxy
	resolver *PresetResolver
}

func (s *presetResolverService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if err := s.resolver.resolveChat(ctx, req); err != nil {
		return nil, err
	}
	return s.Proxy.ProcessChatCompletion(ctx, req)
}

func (s *presetResolverService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	if err := s.resolver.resolveChat(ctx, req); err != nil {
		return nil, err
	}
	return s.Proxy.ProcessChatCompletionStream(ctx, req)
}

// resolveChat resolves the request's preset selection, if any, and merges the
// stored configuration into the request in place.
func (r *PresetResolver) resolveChat(ctx context.Context, req *ChatCompletionRequest) error {
	if req == nil {
		return nil
	}
	name := req.Preset
	usedReference := false
	if strings.HasPrefix(req.Request.Model, PresetReferencePrefix) {
		name = strings.TrimPrefix(req.Request.Model, PresetReferencePrefix)
		usedReference = true
	}
	if name == "" {
		if usedReference {
			return fmt.Errorf("%w: %q names no preset", ErrPresetNotFound, req.Request.Model)
		}
		return nil
	}
	if r == nil || r.source == nil {
		return fmt.Errorf("%w: %q (preset storage is not configured)", ErrPresetNotFound, name)
	}
	record, err := r.source.Get(ctx, name)
	if err != nil {
		if errors.Is(err, presets.ErrNotFound) {
			return fmt.Errorf("%w: %q", ErrPresetNotFound, name)
		}
		return fmt.Errorf("resolve preset %q: %w", name, err)
	}
	mergePresetConfig(req, record.Preset.Config, usedReference)
	return nil
}

// mergePresetConfig applies one preset config to a chat request. The request
// wins: only absent request fields inherit preset values. usedReference
// reports that the request selected the preset through its model field, so
// the preset owns model selection.
func mergePresetConfig(req *ChatCompletionRequest, config presets.Config, usedReference bool) {
	request := &req.Request
	if usedReference || request.Model == "" {
		request.Model = config.Model
		remainder := config.Models
		if request.Model == "" && len(config.Models) > 0 {
			request.Model = config.Models[0]
			remainder = config.Models[1:]
		}
		if len(request.FallbackModels) == 0 && len(remainder) > 0 {
			request.FallbackModels = append([]string(nil), remainder...)
		}
	}

	sampling := &request.Sampling
	if sampling.Temperature == nil {
		sampling.Temperature = config.Temperature
	}
	if sampling.TopP == nil {
		sampling.TopP = config.TopP
	}
	if sampling.PresencePenalty == nil {
		sampling.PresencePenalty = config.PresencePenalty
	}
	if sampling.FrequencyPenalty == nil {
		sampling.FrequencyPenalty = config.FrequencyPenalty
	}
	if sampling.MaxTokens == nil {
		sampling.MaxTokens = config.MaxTokens
	}
	if sampling.Seed == nil {
		sampling.Seed = config.Seed
	}
	if len(sampling.Stop) == 0 && len(config.Stop) > 0 {
		sampling.Stop = append([]string(nil), config.Stop...)
	}

	if config.System != "" && !hasSystemMessage(request.Messages) {
		request.Messages = append([]inference.Message{{
			Role:    inference.RoleSystem,
			Content: []inference.ContentPart{{Kind: inference.ContentText, Text: config.System}},
		}}, request.Messages...)
	}

	if req.Provider == nil && config.Provider != nil {
		allowFallback := true
		if config.Provider.AllowFallbacks != nil {
			allowFallback = *config.Provider.AllowFallbacks
		}
		req.Provider = &ProviderPreferences{
			Order:         append([]string(nil), config.Provider.Order...),
			Only:          append([]string(nil), config.Provider.Only...),
			Ignore:        append([]string(nil), config.Provider.Ignore...),
			AllowFallback: allowFallback,
		}
	}
}

func hasSystemMessage(messages []inference.Message) bool {
	for _, message := range messages {
		if message.Role == inference.RoleSystem {
			return true
		}
	}
	return false
}
