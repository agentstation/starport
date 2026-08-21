package openrouter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuantizationsAccepted(t *testing.T) {
	body := `{
		"model": "openai/gpt-4o",
		"messages": [{"role": "user", "content": "hi"}],
		"provider": {
			"order": ["openai"],
			"quantizations": ["fp8", "int4"],
			"data_collection": "deny",
			"zdr": true,
			"require_parameters": true,
			"experimental": {"force_chat_completions": true},
			"sort": "price",
			"max_price": {"prompt": 1.5, "completion": "3", "image": 0.1, "request": 0.01}
		}
	}`
	decoded, err := DecodeChat(strings.NewReader(body))
	require.NoError(t, err, "documented provider fields must not be rejected")
	require.NotNil(t, decoded.Provider)
	require.Equal(t, []string{"fp8", "int4"}, decoded.Provider.Quantizations)
	require.Equal(t, "price", decoded.Provider.Sort)
	require.NotNil(t, decoded.Provider.MaxPrice)
	require.InDelta(t, 1.5, decoded.Provider.MaxPrice.Prompt, 1e-9)
	require.InDelta(t, 3.0, decoded.Provider.MaxPrice.Completion, 1e-9)

	// Fields Starport cannot yet enforce are accepted and reported, per D3.
	require.Equal(t, []string{
		"data_collection", "experimental", "max_price.image",
		"max_price.request", "quantizations", "require_parameters", "zdr",
	}, decoded.UnenforcedProviderFields)

	// A request that only uses enforced fields reports nothing.
	enforcedOnly := `{
		"model": "openai/gpt-4o",
		"messages": [{"role": "user", "content": "hi"}],
		"provider": {"only": ["openai"], "sort": "throughput", "max_price": {"prompt": 2}}
	}`
	decoded, err = DecodeChat(strings.NewReader(enforcedOnly))
	require.NoError(t, err)
	require.Empty(t, decoded.UnenforcedProviderFields)

	// An unknown sort value is a caller error.
	badSort := `{
		"model": "openai/gpt-4o",
		"messages": [{"role": "user", "content": "hi"}],
		"provider": {"sort": "vibes"}
	}`
	_, err = DecodeChat(strings.NewReader(badSort))
	require.Error(t, err)
	require.Contains(t, err.Error(), "sort")

	// An unknown max_price key is a caller error.
	badPrice := `{
		"model": "openai/gpt-4o",
		"messages": [{"role": "user", "content": "hi"}],
		"provider": {"max_price": {"tokens": 1}}
	}`
	_, err = DecodeChat(strings.NewReader(badPrice))
	require.Error(t, err)
}
