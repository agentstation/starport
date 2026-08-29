package statuspage

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// Incident is one entry in a provider's own published incident log,
// normalized across wire conventions. Every fact in it is the provider's
// assertion about itself; this gateway only carries it.
type Incident struct {
	// Title is the provider's own name for the incident.
	Title string `json:"title"`
	// Indicator is the severity normalized into the poller's vocabulary.
	// Empty when the wire does not state one — an RSS feed says that
	// something happened, not how much of the service it took down.
	Indicator Indicator `json:"indicator,omitempty"`
	// Status is the provider's lifecycle word: investigating, identified,
	// monitoring, resolved on a Statuspage; active or resolved elsewhere.
	// Empty when the wire leaves the state unstated.
	Status string `json:"status,omitempty"`
	// StartedAt is when the provider says the incident began. Zero when
	// the wire carries no parseable timestamp.
	StartedAt time.Time `json:"started_at,omitzero"`
	// ResolvedAt is when the provider closed the incident; zero while open
	// or when the wire does not record closure times.
	ResolvedAt time.Time `json:"resolved_at,omitzero"`
	// URL deep-links to the provider's own page for this incident.
	URL string `json:"url,omitempty"`
	// Update is the provider's latest written update, bounded so a prose
	// postmortem does not become the payload.
	Update string `json:"update,omitempty"`
	// Components are the provider-named services the incident touched.
	Components []string `json:"components,omitempty"`
}

// HistoryAvailability states why a log answer holds the incidents it does,
// so a consumer renders an honest empty state instead of a guessed one.
type HistoryAvailability string

// The availability vocabulary.
const (
	// HistoryAvailable means the provider's log answered; Incidents holds
	// what it published inside the history window, possibly nothing.
	HistoryAvailable HistoryAvailability = "available"
	// HistoryUnpublished means the provider publishes no machine-readable
	// incident log: no declared health API, or a wire convention that
	// carries current status only.
	HistoryUnpublished HistoryAvailability = "unpublished"
	// HistoryUnreachable means a declared log did not answer just now.
	HistoryUnreachable HistoryAvailability = "unreachable"
)

// History is one provider's incident-log answer.
type History struct {
	Availability HistoryAvailability `json:"availability"`
	Incidents    []Incident          `json:"incidents,omitempty"`
	// FetchedAt is when this answer was read from the provider; answers
	// are cached, so it can precede the request that returned it.
	FetchedAt time.Time `json:"fetched_at,omitzero"`
}

// History bounds. The window keeps the log recent enough to mean something
// about the provider today; the caps keep one provider's prose from
// becoming the response body.
const (
	// historyWindow bounds how far back the log reaches.
	historyWindow = 90 * 24 * time.Hour
	// maxHistoryIncidents caps entries per provider.
	maxHistoryIncidents = 25
	// maxHistoryUpdateRunes bounds one incident's latest update text.
	maxHistoryUpdateRunes = 280
	// historyTTL keeps an answered log before re-reading it. Incident
	// history moves on human timescales.
	historyTTL = 5 * time.Minute
	// historyFailureTTL keeps an unreachable verdict briefly, so a dead
	// endpoint is not re-fetched on every page view.
	historyFailureTTL = 30 * time.Second
)

// HistoryReader reads a provider's published incident log on demand and
// caches each answer. It shares the poller's source so a catalog refresh
// changes where history comes from without a restart.
type HistoryReader struct {
	client *http.Client
	source Source
	clock  func() time.Time

	mu    sync.Mutex
	cache map[catalogs.ProviderID]historyEntry
}

type historyEntry struct {
	history   History
	expiresAt time.Time
}

// NewHistoryReader returns a reader over the source. The config supplies
// the request timeout; zero values take the poller defaults.
func NewHistoryReader(config Config, source Source) (*HistoryReader, error) {
	if source == nil {
		return nil, fmt.Errorf("statuspage history reader needs a source")
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = DefaultConfig().RequestTimeout
	}
	return &HistoryReader{
		client: &http.Client{Timeout: config.RequestTimeout},
		source: source,
		clock:  func() time.Time { return time.Now().UTC() },
		cache:  make(map[catalogs.ProviderID]historyEntry),
	}, nil
}

// History answers one provider's incident log, from cache when fresh. A
// provider without a declared health API — or with one whose convention
// carries no history — answers unpublished rather than empty, so the
// consumer can say so instead of implying a clean record.
func (r *HistoryReader) History(ctx context.Context, providerID catalogs.ProviderID) History {
	if r == nil {
		return History{Availability: HistoryUnreachable}
	}
	now := r.clock()
	r.mu.Lock()
	if entry, ok := r.cache[providerID]; ok && now.Before(entry.expiresAt) {
		r.mu.Unlock()
		return entry.history
	}
	r.mu.Unlock()

	history := r.read(ctx, providerID)
	ttl := historyTTL
	if history.Availability == HistoryUnreachable {
		ttl = historyFailureTTL
	}
	r.mu.Lock()
	r.cache[providerID] = historyEntry{history: history, expiresAt: now.Add(ttl)}
	r.mu.Unlock()
	return history
}

func (r *HistoryReader) read(ctx context.Context, providerID catalogs.ProviderID) History {
	target, declared := r.source.HealthAPIs()[providerID]
	if !declared {
		return History{Availability: HistoryUnpublished, FetchedAt: r.clock()}
	}
	var (
		incidents []Incident
		ok        bool
	)
	switch target.Kind {
	case catalogs.HealthAPIKindStatuspage, "":
		incidents, ok = r.historyStatuspage(ctx, target)
	case catalogs.HealthAPIKindRSS:
		incidents, ok = r.historyRSS(ctx, target)
	case catalogs.HealthAPIKindGoogleCloud:
		incidents, ok = r.historyGoogleCloud(ctx, target)
	default:
		// Hyperping publishes no documented incident log, and an unknown
		// kind means this gateway predates the convention. Either way the
		// honest answer is that no log is published to read.
		return History{Availability: HistoryUnpublished, FetchedAt: r.clock()}
	}
	if !ok {
		return History{Availability: HistoryUnreachable, FetchedAt: r.clock()}
	}
	return History{
		Availability: HistoryAvailable,
		Incidents:    boundIncidents(incidents, r.clock()),
		FetchedAt:    r.clock(),
	}
}

// boundIncidents applies the shared history bounds: newest first, inside
// the window, capped. An entry without a parseable start survives the
// window — the wire asserted it happened — and sorts last.
func boundIncidents(incidents []Incident, now time.Time) []Incident {
	cutoff := now.Add(-historyWindow)
	kept := make([]Incident, 0, len(incidents))
	for _, incident := range incidents {
		if !incident.StartedAt.IsZero() && incident.StartedAt.Before(cutoff) {
			continue
		}
		kept = append(kept, incident)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[j].StartedAt.IsZero() {
			return !kept[i].StartedAt.IsZero()
		}
		if kept[i].StartedAt.IsZero() {
			return false
		}
		return kept[i].StartedAt.After(kept[j].StartedAt)
	})
	if len(kept) > maxHistoryIncidents {
		kept = kept[:maxHistoryIncidents]
	}
	return kept
}

// stripMarkup flattens the HTML a feed embeds in its update bodies into
// the plain text an operator reads: tags dropped, entities left alone,
// whitespace collapsed. Each dropped tag leaves a space so adjacent words
// stay apart; the space a closing tag strands before punctuation —
// "underway</em>." — is then pulled back in.
func stripMarkup(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	inTag := false
	for _, r := range text {
		switch {
		case r == '<':
			inTag = true
			builder.WriteRune(' ')
		case r == '>':
			inTag = false
		case !inTag:
			builder.WriteRune(r)
		}
	}
	cleaned := strings.Join(strings.Fields(builder.String()), " ")
	for _, mark := range []string{".", ",", ";", ":", "!", "?", ")"} {
		cleaned = strings.ReplaceAll(cleaned, " "+mark, mark)
	}
	return cleaned
}
