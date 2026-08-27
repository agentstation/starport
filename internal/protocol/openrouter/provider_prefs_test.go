package openrouter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
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

// plugins named work the OpenRouter gateway performs itself, and Starport
// reported the whole field as unkept. That report is now wrong for the one
// plugin this gateway runs.
//
// A caller that reads the header reads it to learn what did not happen. A
// file-parser request had its document read, so naming the field there tells
// the caller the opposite of the truth, and it would send a caller looking for
// a fault that is not there.
func TestAFileParserRequestNamesNoUnenforcedField(t *testing.T) {
	body := `{
		"model": "openai/gpt-4o",
		"messages": [{"role": "user", "content": "hi"}],
		"plugins": [{"id": "file-parser", "pdf": {"engine": "native"}}]
	}`
	decoded, err := DecodeChat(strings.NewReader(body))
	require.NoError(t, err, "a documented OpenRouter field must not be rejected")

	require.Equal(t, inference.ParserEngineNative, decoded.Inference.DocumentParser.Engine,
		"the plugin the header no longer reports has to be the plugin that ran")
	require.Empty(t, decoded.UnenforcedProviderFields,
		"a plugin this gateway enforces was reported as unkept")

	// Extensions travel into the upstream request body verbatim. A gateway
	// field placed there reaches a provider that cannot read it.
	require.NotContains(t, decoded.Inference.Extensions, "plugins")
	require.Empty(t, decoded.Inference.Extensions,
		"a plugins-only request must stay free of extensions, which also keeps it cacheable")
}

// TestAnUnknownPluginIsRefusedRatherThanReported states where the report ended
// and what replaced it.
//
// The plan wrote this case as a report, because an unenforced plugin was a
// header line when the plan was written. PLG1 made it a refusal instead: a
// plugin changes what the model reads, so serving the request without it
// answers a different question and bills the caller for the answer. The
// refusal names the enforced set, which the header never did.
func TestAnUnknownPluginIsRefusedRatherThanReported(t *testing.T) {
	body := `{
		"model": "openai/gpt-4o",
		"messages": [{"role": "user", "content": "hi"}],
		"plugins": [{"id": "web"}]
	}`
	_, err := DecodeChat(strings.NewReader(body))
	require.ErrorIs(t, err, ErrUnenforcedPlugin)
	require.Contains(t, err.Error(), "file-parser",
		"a refusal that names no enforced plugin leaves the caller guessing")
}

// TestTransformsStaysADropInField holds invariant P7 while the plugin beside
// it changes.
//
// transforms names work this gateway does not do, so its report is still the
// truth. The two fields sit in the same list and the same decode path, and a
// change to one of them must not move the other.
func TestTransformsStaysADropInField(t *testing.T) {
	body := `{
		"model": "openai/gpt-4o",
		"messages": [{"role": "user", "content": "hi"}],
		"transforms": ["middle-out"],
		"plugins": [{"id": "file-parser", "pdf": {"engine": "native"}}]
	}`
	decoded, err := DecodeChat(strings.NewReader(body))
	require.NoError(t, err)

	require.Equal(t, []string{"transforms"}, decoded.UnenforcedProviderFields,
		"the enforced plugin and the unkept transform must report separately")
	require.Equal(t, inference.ParserEngineNative, decoded.Inference.DocumentParser.Engine)
	require.NotContains(t, decoded.Inference.Extensions, "transforms",
		"a gateway field must not reach a provider that cannot read it")
}
