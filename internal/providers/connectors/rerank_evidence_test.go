package connectors

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestRerankUsagePresence(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		known       bool
		count       int
	}{
		{"missing", "", false, 0}, {"null", "null", false, 0}, {"zero", "0", true, 0}, {"whole float", "2.0", true, 2},
		{"positive", "38", true, 38}, {"rounded fraction", "1.0000000000000001", false, 0}, {"tiny negative", "-1e-999", false, 0}, {"fraction", "1.5", false, 0}, {"negative", "-1", false, 0}, {"precision boundary", "9007199254740993", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			voyage := `{"data":[]}`
			cohere := `{"results":[]}`
			if tc.value != "" {
				voyage = `{"data":[],"usage":{"total_tokens":` + tc.value + `}}`
				cohere = `{"results":[],"meta":{"billed_units":{"search_units":` + tc.value + `},"tokens":{"input_tokens":` + tc.value + `,"output_tokens":0}}}`
			}
			a, err := (voyageRerankCodec{}).decode(strings.NewReader(voyage))
			require.NoError(t, err)
			require.Equal(t, tc.known, a.TokensKnown)
			b, err := (cohereRerankCodec{}).decode(strings.NewReader(cohere))
			require.NoError(t, err)
			require.Equal(t, tc.known, b.SearchUnitsKnown)
			require.Equal(t, tc.count, b.SearchUnits)
			for _, result := range []*RerankResponse{a, b} {
				canonical, err := RerankResponseToInference(result)
				require.NoError(t, err)
				require.Equal(t, !tc.known, canonical.Usage.TokensUnknown)
				if tc.known {
					require.Equal(t, tc.count, result.Usage.TotalTokens)
				} else {
					require.Nil(t, result.Usage)
				}
			}
		})
	}
}
