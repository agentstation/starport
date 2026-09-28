package router

import (
	"math"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingTokenBounds(t *testing.T) {
	for _, test := range []struct {
		name        string
		input       any
		limit, want int64
	}{
		{"text", "hello", 8192, 8192},
		{"text batch", []string{"hello", "world"}, 8192, 16384},
		{"token IDs", []int{1, 2}, 8192, 8192},
		{"token batch", [][]int{{1, 2}, {3}}, 8192, 16384},
		{"empty text", "", 8192, 0},
		{"empty item", []string{"hello", ""}, 8192, 0},
		{"empty token item", [][]int{{1}, {}}, 8192, 0},
		{"negative ID", [][]int{{-1}}, 8192, 0},
		{"unknown input", map[string]any{"text": "hello"}, 8192, 0},
		{"no input", nil, 8192, 0},
		{"unknown bound", "hello", 0, 0},
		{"overflow", []string{"hello", "world"}, math.MaxInt64, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound, err := embeddingTokenBound(&catalogs.ModelLimits{InputTokens: test.limit}, test.input)
			if test.want == 0 {
				require.ErrorIs(t, err, admission.ErrBoundUnknown)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.want, bound)
		})
	}
	_, err := embeddingTokenBound(nil, "hello")
	require.ErrorIs(t, err, admission.ErrBoundUnknown)
}
