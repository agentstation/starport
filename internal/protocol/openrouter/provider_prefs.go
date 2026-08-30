package openrouter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Sort values the OpenRouter provider-routing policy documents, plus the
// Starport spread extension that balances traffic inside a ranking band.
const (
	SortPrice      = "price"
	SortLatency    = "latency"
	SortThroughput = "throughput"
	SortSpread     = "spread"
)

// MaxPrice is the OpenRouter per-request price ceiling in USD per million
// tokens. OpenRouter accepts each value as a JSON number or a numeric string.
type MaxPrice struct {
	Prompt     float64
	Completion float64
	Image      float64
	Request    float64
}

// UnmarshalJSON accepts the documented max_price object with numeric or
// quoted-numeric values and rejects unknown keys.
func (p *MaxPrice) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("max_price must be an object: %w", err)
	}
	for key, raw := range fields {
		value, err := decodePrice(raw)
		if err != nil {
			return fmt.Errorf("max_price.%s: %w", key, err)
		}
		switch key {
		case "prompt":
			p.Prompt = value
		case "completion":
			p.Completion = value
		case "image":
			p.Image = value
		case "request":
			p.Request = value
		default:
			return fmt.Errorf("max_price key %q is not supported", key)
		}
	}
	return nil
}

func decodePrice(raw json.RawMessage) (float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return 0, err
		}
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, fmt.Errorf("value %q is not a number", text)
		}
		return value, nil
	}
	var value float64
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return 0, fmt.Errorf("value must be a number or numeric string")
	}
	return value, nil
}

func validateProviderPreferences(prefs *ProviderPreferences) error {
	if prefs == nil {
		return nil
	}
	switch prefs.Sort {
	case "", SortPrice, SortLatency, SortThroughput, SortSpread:
		return nil
	default:
		return fmt.Errorf("provider.sort %q is not supported", prefs.Sort)
	}
}

// unenforcedProviderFields lists documented provider fields the request used
// that Starport accepts without enforcement, per the drop-in contract: a
// documented field must not be rejected, and an unkept promise must be loud.
func unenforcedProviderFields(prefs *ProviderPreferences) []string {
	if prefs == nil {
		return nil
	}
	fields := make([]string, 0, 7)
	if len(prefs.Quantizations) > 0 {
		fields = append(fields, "quantizations")
	}
	if prefs.DataCollection != "" {
		fields = append(fields, "data_collection")
	}
	if prefs.ZDR != nil {
		fields = append(fields, "zdr")
	}
	if prefs.RequireParameters != nil {
		fields = append(fields, "require_parameters")
	}
	if len(bytes.TrimSpace(prefs.Experimental)) > 0 && !bytes.Equal(bytes.TrimSpace(prefs.Experimental), []byte("null")) {
		fields = append(fields, "experimental")
	}
	if prefs.MaxPrice != nil {
		if prefs.MaxPrice.Image > 0 {
			fields = append(fields, "max_price.image")
		}
		if prefs.MaxPrice.Request > 0 {
			fields = append(fields, "max_price.request")
		}
	}
	if len(fields) == 0 {
		return nil
	}
	sort.Strings(fields)
	return fields
}

// unenforcedGatewayFields lists top-level OpenRouter fields that name work
// the OpenRouter gateway performs on the caller's behalf. Starport routes to
// providers directly, so no upstream understands them. They follow the same
// drop-in contract as the provider fields above: accept, do not forward, and
// report the unkept promise.
//
// The plugins field is not one of them any more. This gateway enforces
// file-parser, and it refuses every other identifier at the door, so a request
// that reaches here with plugins named work that ran. Reporting it as unkept
// would tell a caller its document was never read, which is the opposite of
// what happened, and a wrong record is worse than a missing one.
func unenforcedGatewayFields(wire *ChatRequest) []string {
	if wire == nil {
		return nil
	}
	fields := make([]string, 0, 1)
	if len(wire.Transforms) > 0 {
		fields = append(fields, "transforms")
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// unenforcedFields merges the gateway-level and provider-level unkept promises
// into the single sorted list the caller reads from one response header.
func unenforcedFields(wire *ChatRequest) []string {
	if wire == nil {
		return nil
	}
	fields := append(unenforcedGatewayFields(wire), unenforcedProviderFields(wire.Provider)...)
	if len(fields) == 0 {
		return nil
	}
	sort.Strings(fields)
	return fields
}
