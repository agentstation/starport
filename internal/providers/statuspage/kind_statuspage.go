package statuspage

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"strings"
	"time"
)

// maxStatuspageBytes caps a summary or components response. The largest
// real components document among catalog providers is tens of kilobytes.
const maxStatuspageBytes = 512 * 1024

// summaryDocument is the part of the statuspage.io summary this poller reads.
type summaryDocument struct {
	Status struct {
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
}

// componentsDocument is the statuspage.io components listing. It is read
// instead of the summary when the catalog names components, because the
// summary omits components without an open incident attached — a degraded
// component can be missing from it entirely.
type componentsDocument struct {
	Components []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"components"`
}

// statuspageComponentIndicator maps the component status vocabulary onto
// the severity set. Maintenance reads as minor: the component is impaired
// for a reason, but on purpose.
func statuspageComponentIndicator(status string) (Indicator, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "operational":
		return IndicatorNone, true
	case "degraded_performance", "under_maintenance":
		return IndicatorMinor, true
	case "partial_outage":
		return IndicatorMajor, true
	case "major_outage":
		return IndicatorCritical, true
	default:
		return IndicatorNone, false
	}
}

// readStatuspage answers from the Atlassian Statuspage API. With catalog
// components it targets exactly the services this gateway routes to through
// the components document; without them, or when the components document
// does not answer, the page-wide summary indicator stands in.
func (p *Poller) readStatuspage(ctx context.Context, target Target) (verdict, bool) {
	if len(target.Components) > 0 {
		if answer, ok := p.readStatuspageComponents(ctx, target); ok {
			return answer, true
		}
	}
	return p.readStatuspageSummary(ctx, target.URL)
}

func (p *Poller) readStatuspageComponents(ctx context.Context, target Target) (verdict, bool) {
	body, ok := p.fetch(ctx, siblingDocumentURL(target.URL, "components.json"), maxStatuspageBytes)
	if !ok {
		return verdict{}, false
	}
	var document componentsDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return verdict{}, false
	}
	targeted := make(map[string]struct{}, len(target.Components))
	for _, component := range target.Components {
		targeted[component.ID] = struct{}{}
	}
	matched := false
	answer := verdict{indicator: IndicatorNone}
	for _, component := range document.Components {
		if _, wanted := targeted[component.ID]; !wanted {
			continue
		}
		indicator, known := statuspageComponentIndicator(component.Status)
		if !known {
			continue
		}
		matched = true
		if indicatorRank(indicator) > indicatorRank(answer.indicator) {
			answer.indicator = indicator
			answer.description = describeComponent(component.Name, component.Status)
		}
	}
	// A document that names none of the catalog's components answers a
	// different question; let the summary answer instead.
	return answer, matched
}

func (p *Poller) readStatuspageSummary(ctx context.Context, apiURL string) (verdict, bool) {
	body, ok := p.fetch(ctx, apiURL, maxStatuspageBytes)
	if !ok {
		return verdict{}, false
	}
	var document summaryDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return verdict{}, false
	}
	indicator := Indicator(strings.ToLower(strings.TrimSpace(document.Status.Indicator)))
	if indicator == "maintenance" {
		indicator = IndicatorMinor
	}
	switch indicator {
	case IndicatorNone, IndicatorMinor, IndicatorMajor, IndicatorCritical:
	default:
		return verdict{}, false
	}
	return verdict{
		indicator:   indicator,
		description: strings.TrimSpace(document.Status.Description),
	}, true
}

// maxStatuspageIncidentBytes caps the incidents document. It carries the
// page's last 50 incidents with their full update threads; the largest
// among catalog providers measures ~200 KB today.
const maxStatuspageIncidentBytes = 2 * 1024 * 1024

// incidentsDocument is the part of the statuspage.io incidents listing the
// history reader uses. The API returns the page's recent incidents newest
// first, each with its update thread in the same order.
type incidentsDocument struct {
	Incidents []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Impact     string `json:"impact"`
		StartedAt  string `json:"started_at"`
		CreatedAt  string `json:"created_at"`
		ResolvedAt string `json:"resolved_at"`
		Shortlink  string `json:"shortlink"`
		Components []struct {
			Name string `json:"name"`
		} `json:"components"`
		IncidentUpdates []struct {
			Body string `json:"body"`
		} `json:"incident_updates"`
	} `json:"incidents"`
}

// historyStatuspage reads the incidents document beside the declared
// summary URL. Impact maps one-to-one onto the severity vocabulary because
// the vocabulary is the Statuspage one; an impact of "none" or an unknown
// word stays unstated rather than asserting a severity the page did not.
func (r *HistoryReader) historyStatuspage(ctx context.Context, target Target) ([]Incident, bool) {
	body, ok := fetchDocument(ctx, r.client, siblingDocumentURL(target.URL, "incidents.json"), maxStatuspageIncidentBytes)
	if !ok {
		return nil, false
	}
	var document incidentsDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, false
	}
	incidents := make([]Incident, 0, len(document.Incidents))
	for _, entry := range document.Incidents {
		incident := Incident{
			Title:      strings.TrimSpace(entry.Name),
			Status:     strings.ToLower(strings.TrimSpace(entry.Status)),
			StartedAt:  parseStatuspageTime(entry.StartedAt, entry.CreatedAt),
			ResolvedAt: parseStatuspageTime(entry.ResolvedAt),
			URL:        strings.TrimSpace(entry.Shortlink),
		}
		switch impact := Indicator(strings.ToLower(strings.TrimSpace(entry.Impact))); impact {
		case IndicatorMinor, IndicatorMajor, IndicatorCritical:
			incident.Indicator = impact
		}
		if len(entry.IncidentUpdates) > 0 {
			incident.Update = truncateRunes(stripMarkup(entry.IncidentUpdates[0].Body), maxHistoryUpdateRunes)
		}
		for _, component := range entry.Components {
			if name := strings.TrimSpace(component.Name); name != "" {
				incident.Components = append(incident.Components, name)
			}
		}
		incidents = append(incidents, incident)
	}
	return incidents, true
}

// parseStatuspageTime reads the first candidate that parses. Statuspage
// timestamps are RFC 3339 with milliseconds.
func parseStatuspageTime(candidates ...string) time.Time {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if stamp, err := time.Parse(time.RFC3339, candidate); err == nil {
			return stamp.UTC()
		}
	}
	return time.Time{}
}

// siblingDocumentURL swaps the final path segment of a health API URL for a
// sibling document's name, turning .../api/v2/summary.json into
// .../api/v2/components.json.
func siblingDocumentURL(apiURL, name string) string {
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return apiURL
	}
	parsed.Path = path.Join(path.Dir(parsed.Path), name)
	return parsed.String()
}
