package router

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestRerankTokenBound(t *testing.T) {
	base := func() *catalogs.ModelLimits {
		return &catalogs.ModelLimits{ContextWindow: 32000, InputTokens: 600000, MaxDocuments: 1000}
	}
	for _, tc := range []struct {
		name   string
		count  int
		change func(*catalogs.ModelLimits, *connectors.RerankRequest)
		want   int64
	}{
		{name: "one pair", count: 1, want: 32000},
		{name: "top one still bills two pairs", count: 2, want: 64000},
		{name: "request cap", count: 20, want: 600000},
		{name: "maximum documents", count: 1000, want: 600000},
		{name: "too many documents", count: 1001},
		{name: "no documents"},
		{name: "empty query", count: 1, change: func(_ *catalogs.ModelLimits, r *connectors.RerankRequest) { r.Query = "" }},
		{name: "empty document", count: 1, change: func(_ *catalogs.ModelLimits, r *connectors.RerankRequest) { r.Documents[0] = "" }},
		{name: "unsupported cap", count: 1, change: func(_ *catalogs.ModelLimits, r *connectors.RerankRequest) { r.MaxTokensPerDocument = new(10) }},
		{name: "missing context", count: 1, change: func(l *catalogs.ModelLimits, _ *connectors.RerankRequest) { l.ContextWindow = 0 }},
		{name: "missing total", count: 1, change: func(l *catalogs.ModelLimits, _ *connectors.RerankRequest) { l.InputTokens = 0 }},
		{name: "unknown total", count: 1, change: func(l *catalogs.ModelLimits, _ *connectors.RerankRequest) {
			l.SetUnknown(catalogs.ModelLimitInputTokens)
		}},
		{name: "saturates without overflow", count: 2, want: math.MaxInt64, change: func(l *catalogs.ModelLimits, _ *connectors.RerankRequest) {
			l.ContextWindow = math.MaxInt64
			l.InputTokens = math.MaxInt64
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := base()
			r := &connectors.RerankRequest{Query: "query", Documents: make([]string, tc.count), TopN: new(1)}
			for i := range r.Documents {
				r.Documents[i] = "document"
			}
			if tc.change != nil {
				tc.change(l, r)
			}
			bound, err := rerankTokenBound(l, r)
			if tc.want == 0 {
				require.ErrorIs(t, err, admission.ErrBoundUnknown)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, bound)
		})
	}
}
