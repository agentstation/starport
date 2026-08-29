package statuspage

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// maxGoogleCloudBytes caps the Google Cloud incidents document. It carries
// a rolling incident history for every product, so it runs to megabytes.
const maxGoogleCloudBytes = 16 * 1024 * 1024

// maxGoogleDescriptionRunes bounds the projected incident line. Google's
// external descriptions are prose paragraphs, not status lines.
const maxGoogleDescriptionRunes = 240

// googleIncident is the part of one incidents.json entry this package
// reads. An incident is open while its end timestamp is empty.
type googleIncident struct {
	Begin            string `json:"begin"`
	End              string `json:"end"`
	ExternalDesc     string `json:"external_desc"`
	StatusImpact     string `json:"status_impact"`
	URI              string `json:"uri"`
	MostRecentUpdate struct {
		Text string `json:"text"`
	} `json:"most_recent_update"`
	AffectedProducts []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"affected_products"`
}

// googleIndicator maps the status_impact vocabulary onto the severity set.
func googleIndicator(impact string) (Indicator, bool) {
	switch strings.ToUpper(strings.TrimSpace(impact)) {
	case "SERVICE_OUTAGE":
		return IndicatorCritical, true
	case "SERVICE_DISRUPTION":
		return IndicatorMajor, true
	case "SERVICE_INFORMATION":
		return IndicatorMinor, true
	default:
		return IndicatorNone, false
	}
}

// readGoogleCloud answers from the Google Cloud service health document: a
// history of incidents across every product, filtered here to open
// incidents touching the catalog's products. The catalog must name
// components — without them every unrelated product's incident would speak
// for this provider.
func (p *Poller) readGoogleCloud(ctx context.Context, target Target) (verdict, bool) {
	if len(target.Components) == 0 {
		return verdict{}, false
	}
	body, ok := p.fetch(ctx, target.URL, maxGoogleCloudBytes)
	if !ok {
		return verdict{}, false
	}
	var incidents []googleIncident
	if err := json.Unmarshal(body, &incidents); err != nil {
		return verdict{}, false
	}
	targeted := make(map[string]struct{}, len(target.Components))
	for _, component := range target.Components {
		targeted[component.ID] = struct{}{}
	}
	answer := verdict{indicator: IndicatorNone}
	for _, incident := range incidents {
		if strings.TrimSpace(incident.End) != "" {
			continue
		}
		touches := false
		for _, product := range incident.AffectedProducts {
			if _, wanted := targeted[product.ID]; wanted {
				touches = true
				break
			}
		}
		if !touches {
			continue
		}
		indicator, known := googleIndicator(incident.StatusImpact)
		if !known {
			continue
		}
		if indicatorRank(indicator) > indicatorRank(answer.indicator) {
			answer.indicator = indicator
			answer.description = truncateRunes(strings.TrimSpace(incident.ExternalDesc), maxGoogleDescriptionRunes)
		}
	}
	return answer, true
}

// historyGoogleCloud reads the same document as the poller but keeps the
// history: open and closed incidents alike, still filtered to the
// catalog's products because the feed speaks for a whole cloud. The same
// component rule applies — without declared products there is no log to
// answer honestly, so the reader reports the fetch as failed rather than
// serving every unrelated product's incidents.
func (r *HistoryReader) historyGoogleCloud(ctx context.Context, target Target) ([]Incident, bool) {
	if len(target.Components) == 0 {
		return nil, false
	}
	body, ok := fetchDocument(ctx, r.client, target.URL, maxGoogleCloudBytes)
	if !ok {
		return nil, false
	}
	var entries []googleIncident
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, false
	}
	targeted := make(map[string]struct{}, len(target.Components))
	for _, component := range target.Components {
		targeted[component.ID] = struct{}{}
	}
	incidents := make([]Incident, 0, len(entries))
	for _, entry := range entries {
		var touched []string
		for _, product := range entry.AffectedProducts {
			if _, wanted := targeted[product.ID]; wanted {
				touched = append(touched, strings.TrimSpace(product.Title))
			}
		}
		if len(touched) == 0 {
			continue
		}
		incident := Incident{
			Title:      truncateRunes(strings.TrimSpace(entry.ExternalDesc), maxGoogleDescriptionRunes),
			StartedAt:  parseGoogleTime(entry.Begin),
			ResolvedAt: parseGoogleTime(entry.End),
			URL:        googleIncidentURL(target.URL, entry.URI),
			Update:     truncateRunes(stripMarkup(entry.MostRecentUpdate.Text), maxHistoryUpdateRunes),
			Components: touched,
		}
		if incident.ResolvedAt.IsZero() {
			incident.Status = "active"
		} else {
			incident.Status = "resolved"
		}
		if indicator, known := googleIndicator(entry.StatusImpact); known {
			incident.Indicator = indicator
		}
		incidents = append(incidents, incident)
	}
	return incidents, true
}

// parseGoogleTime reads one incidents.json timestamp: RFC 3339 with a
// numeric zone offset.
func parseGoogleTime(stamp string) time.Time {
	stamp = strings.TrimSpace(stamp)
	if stamp == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, stamp); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

// googleIncidentURL resolves the entry's relative uri — "incidents/<id>" —
// against the host serving the feed.
func googleIncidentURL(feedURL, uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	parsed, err := url.Parse(feedURL)
	if err != nil {
		return ""
	}
	parsed.Path = "/" + strings.TrimPrefix(uri, "/")
	parsed.RawQuery = ""
	return parsed.String()
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
