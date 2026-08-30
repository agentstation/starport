// Package events owns the gateway's outbound webhook surface: the event
// names, the signed envelope, and delivery to configured endpoints with
// bounded retry. Nothing pushes until configuration names an endpoint,
// and a payload carries identifiers and states only — never a provider
// credential, gateway key material, or prompt and response content.
package events

// Event type names. Each follows concept.verb, the naming the audit
// trail's actions use, so an operator reads one vocabulary across both
// surfaces.
const (
	// TypeBudgetExhausted fires when a budget refuses a request: the
	// window's spend or token meter is spent and the caller drew a 402.
	TypeBudgetExhausted = "budget.exhausted"
	// TypeJobCompleted fires once when an asynchronous job produced its
	// asset.
	TypeJobCompleted = "job.completed"
	// TypeJobFailed fires once when an asynchronous job ended without an
	// asset.
	TypeJobFailed = "job.failed"
	// TypeJobCancelled fires once when a job's owner stopped it.
	TypeJobCancelled = "job.cancelled"
	// TypeProviderHealthChanged fires when a provider's status-page
	// indicator moved: an incident opened, worsened, or cleared.
	TypeProviderHealthChanged = "provider.health.changed"
	// TypeKeyCreated fires when the admin surface issues a gateway API
	// key. The payload names the key; it never carries the token.
	TypeKeyCreated = "key.created"
	// TypeKeyDeleted fires when the admin surface revokes a gateway API
	// key.
	TypeKeyDeleted = "key.deleted"
)

// Event is the envelope one delivery carries, encoded as one JSON object.
// Data holds short identifier and state strings only.
type Event struct {
	// ID names this event. A receiver deduplicates redeliveries on it.
	ID string `json:"id"`
	// Type is one of the Type constants above.
	Type string `json:"type"`
	// Time is when the gateway observed the fact, RFC 3339 UTC.
	Time string `json:"time"`
	// Data is the typed payload: identifiers, scopes, and states.
	Data map[string]string `json:"data"`
}

// TypeForJobState maps a terminal job state onto its event name. The job
// seam reports states without naming events, so the mapping lives here
// with the rest of the vocabulary. An unknown state maps to TypeJobFailed:
// a receiver told nothing about an ended job is worse than one told the
// conservative verdict.
func TypeForJobState(state string) string {
	switch state {
	case "completed":
		return TypeJobCompleted
	case "cancelled":
		return TypeJobCancelled
	default:
		return TypeJobFailed
	}
}
