package statuspage

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// maxGoogleCloudBytes caps the Google Cloud incidents document. It carries
// a rolling incident history for every product, so it runs to megabytes.
const maxGoogleCloudBytes = 16 * 1024 * 1024

// maxGoogleDescriptionRunes bounds the projected incident line. Google's
// external descriptions are prose paragraphs, not status lines.
const maxGoogleDescriptionRunes = 240

// googleIncident is the part of one incidents.json entry this poller
// reads. An incident is open while its end timestamp is empty.
type googleIncident struct {
	End              string `json:"end"`
	ExternalDesc     string `json:"external_desc"`
	StatusImpact     string `json:"status_impact"`
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

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
