package proxy

import (
	"github.com/agentstation/starport/internal/failure"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRecognitionPreservesAdmissionFailure(t *testing.T) {
	for _, kind := range []failure.Kind{failure.Quota, failure.GatewayUnavailable} {
		t.Run(string(kind), func(t *testing.T) {
			original := failure.New(kind, "admission refused", false, failure.ProviderDetails{}, nil)
			require.Same(t, original, recognitionFailure("scanned.pdf", original))
		})
	}
}
