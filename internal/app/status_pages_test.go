package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	providerstate "github.com/agentstation/starport/internal/providers/state"
	"github.com/agentstation/starport/internal/providers/statuspage"
)

// recordingEmitter keeps every event the publisher pushes, so the test
// reads what a webhook receiver would.
type recordingEmitter struct {
	types    []string
	payloads []map[string]string
}

func (e *recordingEmitter) Emit(eventType string, data map[string]string) {
	e.types = append(e.types, eventType)
	e.payloads = append(e.payloads, data)
}

// A provider whose status page moves to an incident emits one named
// event per transition, and a pass that re-confirms the same indicator
// emits nothing: the export surface carries changes, not weather.
func TestAHealthTransitionEmitsOneNamedEvent(t *testing.T) {
	t.Parallel()

	emitter := &recordingEmitter{}
	publisher := providerIncidentPublisher{
		states: providerstate.New(),
		events: emitter,
	}
	observed := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	observation := statuspage.Observation{
		ProviderID:  "openai",
		Indicator:   statuspage.Indicator("major"),
		Description: "Elevated error rates",
		CheckedAt:   observed,
	}

	publisher.PublishIncidents([]statuspage.Observation{observation})
	require.Equal(t, []string{"provider.health.changed"}, emitter.types)
	payload := emitter.payloads[0]
	assert.Equal(t, "openai", payload["provider"])
	assert.Equal(t, "major", payload["indicator"])
	assert.Equal(t, "Elevated error rates", payload["description"])
	assert.Equal(t, "2026-08-30T12:00:00Z", payload["observed_at"])

	// The same indicator again is not a transition.
	publisher.PublishIncidents([]statuspage.Observation{observation})
	assert.Len(t, emitter.types, 1)
}
