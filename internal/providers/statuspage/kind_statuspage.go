package statuspage

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"strings"
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
