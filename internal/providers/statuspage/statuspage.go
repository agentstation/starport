// Package statuspage observes provider service incidents from each
// provider's own published status page. The page URL is a catalog fact; the
// live indicator read from it is availability evidence this gateway owns.
// An observation never blocks or reorders routing — it is operator-facing
// evidence beside the gateway's own request measurements.
package statuspage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// Indicator is the statuspage.io summary vocabulary. A page that does not
// answer the summary endpoint yields no observation at all, never a guessed
// indicator.
type Indicator string

// Indicators are the closed set the summary endpoint publishes.
const (
	IndicatorNone     Indicator = "none"
	IndicatorMinor    Indicator = "minor"
	IndicatorMajor    Indicator = "major"
	IndicatorCritical Indicator = "critical"
)

// Observation is one provider's answered status-page verdict.
type Observation struct {
	ProviderID  catalogs.ProviderID
	Indicator   Indicator
	Description string
	CheckedAt   time.Time
}

// Source names the providers to observe and each one's status page URL.
// The catalog owns both facts; the poller reads them fresh each pass so a
// catalog refresh changes what is polled without a restart.
type Source interface {
	StatusPages() map[catalogs.ProviderID]string
}

// Publisher accepts one complete observation pass. Providers whose pages did
// not answer are absent, and the publisher replaces its whole projection, so
// a page that stops answering stops asserting anything.
type Publisher interface {
	PublishIncidents([]Observation)
}

// Config bounds the poll loop.
type Config struct {
	// Interval separates passes. The default is a minute: incidents move on
	// human timescales, and a status page is someone else's infrastructure.
	Interval time.Duration
	// RequestTimeout bounds one page fetch.
	RequestTimeout time.Duration
	// MaxConcurrent bounds simultaneous fetches in one pass.
	MaxConcurrent int
}

// DefaultConfig returns the bounds a deployment starts with.
func DefaultConfig() Config {
	return Config{Interval: time.Minute, RequestTimeout: 5 * time.Second, MaxConcurrent: 8}
}

// maxSummaryBytes caps a summary response. The real document is under a
// kilobyte; anything larger is not the document this poller asked for.
const maxSummaryBytes = 64 * 1024

// summaryPath is the statuspage.io summary convention most inference
// providers publish. A page on other software answers 404 or HTML, and the
// poller records nothing for it.
const summaryPath = "/api/v2/status.json"

// Poller reads every named status page on an interval and publishes the
// pages that answered.
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

// PollOnce fetches every named page and publishes the complete pass.
func (p *Poller) PollOnce(ctx context.Context) {
	pages := p.source.StatusPages()
	if len(pages) == 0 {
		p.publisher.PublishIncidents(nil)
		return
	}
	var mu sync.Mutex
	observations := make([]Observation, 0, len(pages))
	limiter := make(chan struct{}, p.config.MaxConcurrent)
	var wg sync.WaitGroup
	for providerID, pageURL := range pages {
		wg.Add(1)
		limiter <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-limiter }()
			observation, ok := p.fetch(ctx, providerID, pageURL)
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

// summaryDocument is the part of the statuspage.io summary this poller reads.
type summaryDocument struct {
	Status struct {
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
}

func (p *Poller) fetch(ctx context.Context, providerID catalogs.ProviderID, pageURL string) (Observation, bool) {
	endpoint := strings.TrimRight(strings.TrimSpace(pageURL), "/") + summaryPath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Observation{}, false
	}
	request.Header.Set("Accept", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return Observation{}, false
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Observation{}, false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSummaryBytes))
	if err != nil {
		return Observation{}, false
	}
	var document summaryDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return Observation{}, false
	}
	indicator := Indicator(strings.ToLower(strings.TrimSpace(document.Status.Indicator)))
	switch indicator {
	case IndicatorNone, IndicatorMinor, IndicatorMajor, IndicatorCritical:
	default:
		return Observation{}, false
	}
	return Observation{
		ProviderID:  providerID,
		Indicator:   indicator,
		Description: strings.TrimSpace(document.Status.Description),
		CheckedAt:   p.clock(),
	}, true
}
