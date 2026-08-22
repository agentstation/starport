package logos

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBytesServesEveryBundledMark(t *testing.T) {
	for _, kind := range []Kind{KindProviders, KindAuthors} {
		entries, err := files.ReadDir("svg/" + string(kind))
		require.NoError(t, err)
		require.NotEmpty(t, entries)
		for _, entry := range entries {
			id := strings.TrimSuffix(entry.Name(), ".svg")
			svg, etag, ok := Bytes(kind, id)
			require.True(t, ok, "%s/%s", kind, id)
			require.NotEmpty(t, etag)
			require.Contains(t, string(svg), "<svg", "%s/%s", kind, id)
		}
	}
}

func TestBytesCoversCatalogProviders(t *testing.T) {
	// The 15 providers the catalog ships today. A new provider without a
	// bundled mark falls back to initials in the console, so this list
	// documents coverage rather than gating catalog growth.
	for _, id := range []string{
		"alibaba", "anthropic", "azure-openai", "cerebras", "deepinfra",
		"deepseek", "fireworks-ai", "google-ai-studio", "google-vertex",
		"groq", "hetzner", "mistral", "moonshot-ai", "ollama", "openai",
	} {
		_, _, ok := Bytes(KindProviders, id)
		require.True(t, ok, id)
	}
}

func TestBytesUnknownAndInvalid(t *testing.T) {
	for _, tc := range []struct {
		kind Kind
		id   string
	}{
		{KindProviders, "no-such-provider"},
		{KindAuthors, ""},
		{Kind("models"), "openai"},
		{KindProviders, "../logos"},
		{KindProviders, ".."},
		{KindProviders, "OpenAI"},
	} {
		_, _, ok := Bytes(tc.kind, tc.id)
		require.False(t, ok, "%s/%s", tc.kind, tc.id)
	}
}

func TestETagIsStablePerContent(t *testing.T) {
	first, etag1, ok := Bytes(KindProviders, "openai")
	require.True(t, ok)
	second, etag2, ok := Bytes(KindProviders, "openai")
	require.True(t, ok)
	require.Equal(t, first, second)
	require.Equal(t, etag1, etag2)

	_, other, ok := Bytes(KindProviders, "groq")
	require.True(t, ok)
	require.NotEqual(t, etag1, other)
}
