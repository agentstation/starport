// Package statuspage observes provider service incidents from each
// provider's own published health API. The API location, its wire
// convention, and the service components that map to this gateway's
// endpoints are catalog facts; the live indicator read from them is
// availability evidence this gateway owns. An observation never blocks or
// reorders routing — it is operator-facing evidence beside the gateway's
// own request measurements.
package statuspage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// Indicator is the severity vocabulary every health API convention is
// normalized into. It is the statuspage.io summary vocabulary because that
// is the convention most providers publish. An API that does not answer
// yields no observation at all, never a guessed indicator.
type Indicator string

// Indicators are the closed severity set.
const (
	IndicatorNone     Indicator = "none"
	IndicatorMinor    Indicator = "minor"
	IndicatorMajor    Indicator = "major"
	IndicatorCritical Indicator = "critical"
)

// indicatorRank orders severities so a pass over many components keeps the
// worst one. An unknown word ranks below none and never wins.
func indicatorRank(indicator Indicator) int {
	switch indicator {
	case IndicatorNone:
		return 1
	case IndicatorMinor:
		return 2
	case IndicatorMajor:
		return 3
	case IndicatorCritical:
		return 4
	default:
		return 0
	}
}

// Observation is one provider's answered health verdict.
type Observation struct {
	ProviderID  catalogs.ProviderID
	Indicator   Indicator
	Description string
	CheckedAt   time.Time
}

// Target is one provider's catalog-declared health API: where it answers,
// which wire convention it speaks, and which of its components serve the
// endpoints this gateway routes to. An empty kind means statuspage, the
// convention every entry used before the kind existed.
type Target struct {
	URL        string
	Kind       catalogs.HealthAPIKind
	Components []catalogs.ProviderHealthComponent
}

// Source names the providers to observe and each one's health API target.
// The catalog owns every fact in a target; the poller reads them fresh each
// pass so a catalog refresh changes what is polled without a restart.
type Source interface {
	HealthAPIs() map[catalogs.ProviderID]Target
}

// Publisher accepts one complete observation pass. Providers whose APIs did
// not answer are absent, and the publisher replaces its whole projection, so
// an API that stops answering stops asserting anything.
type Publisher interface {
	PublishIncidents([]Observation)
}

// Config bounds the poll loop.
type Config struct {
	// Interval separates passes. The default is a minute: incidents move on
	// human timescales, and a health API is someone else's infrastructure.
	Interval time.Duration
	// RequestTimeout bounds one API fetch.
	RequestTimeout time.Duration
	// MaxConcurrent bounds simultaneous fetches in one pass.
	MaxConcurrent int
}

// DefaultConfig returns the bounds a deployment starts with.
func DefaultConfig() Config {
	return Config{Interval: time.Minute, RequestTimeout: 5 * time.Second, MaxConcurrent: 8}
}

// Poller reads every declared health API on an interval and publishes the
// targets that answered.
type Poller struct {
	config    Config
	client    *http.Client
	source    Source
	publisher Publisher
	clock     func() time.Time
}

// New returns a poller over the source and publisher.
func New(config Config, source Source, publisher Publisher) (*Poller, error) {
	if source == nil || publisher == nil {
		return nil, fmt.Errorf("statuspage poller needs a source and a publisher")
	}
	defaults := DefaultConfig()
	if config.Interval <= 0 {
		config.Interval = defaults.Interval
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = defaults.RequestTimeout
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = defaults.MaxConcurrent
	}
	return &Poller{
		config:    config,
		client:    &http.Client{Timeout: config.RequestTimeout},
		source:    source,
		publisher: publisher,
		clock:     func() time.Time { return time.Now().UTC() },
	}, nil
}

// Run polls once at start and then once per interval until the context ends.
func (p *Poller) Run(ctx context.Context) {
	p.PollOnce(ctx)
	ticker := time.NewTicker(p.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.PollOnce(ctx)
		}
	}
}

// PollOnce fetches every declared health API and publishes the complete pass.
func (p *Poller) PollOnce(ctx context.Context) {
	targets := p.source.HealthAPIs()
	if len(targets) == 0 {
		p.publisher.PublishIncidents(nil)
		return
	}
	var mu sync.Mutex
	observations := make([]Observation, 0, len(targets))
	limiter := make(chan struct{}, p.config.MaxConcurrent)
	var wg sync.WaitGroup
	for providerID, target := range targets {
		wg.Add(1)
		limiter <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-limiter }()
			observation, ok := p.observe(ctx, providerID, target)
			if !ok {
				return
			}
			mu.Lock()
			observations = append(observations, observation)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	p.publisher.PublishIncidents(observations)
}

// observe dispatches one target to the reader for its wire convention. An
// unknown kind contributes nothing: the catalog validated the kind at build
// time, so an unrecognized word here means this gateway predates it, and a
// guess would assert evidence the gateway cannot read.
func (p *Poller) observe(ctx context.Context, providerID catalogs.ProviderID, target Target) (Observation, bool) {
	verdict, ok := p.read(ctx, target)
	if !ok {
		return Observation{}, false
	}
	return Observation{
		ProviderID:  providerID,
		Indicator:   verdict.indicator,
		Description: verdict.description,
		CheckedAt:   p.clock(),
	}, true
}

// verdict is one health API's normalized answer before it is stamped with
// the provider and the clock.
type verdict struct {
	indicator   Indicator
	description string
}

func (p *Poller) read(ctx context.Context, target Target) (verdict, bool) {
	switch target.Kind {
	case catalogs.HealthAPIKindStatuspage, "":
		return p.readStatuspage(ctx, target)
	case catalogs.HealthAPIKindHyperping:
		return p.readHyperping(ctx, target)
	case catalogs.HealthAPIKindRSS:
		return p.readRSS(ctx, target)
	case catalogs.HealthAPIKindGoogleCloud:
		return p.readGoogleCloud(ctx, target)
	default:
		return verdict{}, false
	}
}

// fetch reads one URL up to maxBytes. Anything but a 200 with a readable
// body is no evidence at all.
func (p *Poller) fetch(ctx context.Context, url string, maxBytes int64) ([]byte, bool) {
	return fetchDocument(ctx, p.client, url, maxBytes)
}

// fetchDocument is the one health-API read both the poller and the history
// reader use, so every convention shares the same evidence rule.
func fetchDocument(ctx context.Context, client *http.Client, url string, maxBytes int64) ([]byte, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	request.Header.Set("Accept", "application/json, application/rss+xml, application/atom+xml, application/xml")
	response, err := client.Do(request)
	if err != nil {
		return nil, false
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes))
	if err != nil {
		return nil, false
	}
	return body, true
}

// describeComponent joins a component name and its reported state into the
// operator-facing incident line, with the wire word's underscores opened up.
func describeComponent(name, state string) string {
	state = strings.ReplaceAll(strings.TrimSpace(state), "_", " ")
	name = strings.TrimSpace(name)
	if name == "" {
		return state
	}
	return name + ": " + state
}
