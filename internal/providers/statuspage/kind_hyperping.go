package statuspage

import (
	"context"
	"encoding/json"
	"strings"
)

// maxHyperpingBytes caps a Hyperping status document. The real document
// lists every monitored service and model and stays well under this.
const maxHyperpingBytes = 512 * 1024

// hyperpingDocument is the Hyperping status JSON (schemaVersion 1). The
// services and models arrays share one entry shape, and a catalog component
// id is the entry's publicId.
type hyperpingDocument struct {
	OverallStatus string             `json:"overallStatus"`
	Services      []hyperpingService `json:"services"`
	Models        []hyperpingService `json:"models"`
}

type hyperpingService struct {
	PublicID    string `json:"publicId"`
	DisplayName string `json:"displayName"`
	Status      string `json:"status"`
}

// hyperpingIndicator maps a Hyperping status word. The vocabulary is not
// published as a closed set, so anything that is not OPERATIONAL reads as a
// minor incident: real evidence of impairment, asserted conservatively.
func hyperpingIndicator(status string) Indicator {
	if strings.EqualFold(strings.TrimSpace(status), "OPERATIONAL") {
		return IndicatorNone
	}
	return IndicatorMinor
}

// readHyperping answers from a Hyperping status document. With catalog
// components it reads exactly those entries; without them the overall
// status stands for the page.
func (p *Poller) readHyperping(ctx context.Context, target Target) (verdict, bool) {
	body, ok := p.fetch(ctx, target.URL, maxHyperpingBytes)
	if !ok {
		return verdict{}, false
	}
	var document hyperpingDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return verdict{}, false
	}
	if len(target.Components) == 0 {
		if strings.TrimSpace(document.OverallStatus) == "" {
			return verdict{}, false
		}
		answer := verdict{indicator: hyperpingIndicator(document.OverallStatus)}
		if answer.indicator != IndicatorNone {
			answer.description = describeComponent("", document.OverallStatus)
		}
		return answer, true
	}
	targeted := make(map[string]struct{}, len(target.Components))
	for _, component := range target.Components {
		targeted[component.ID] = struct{}{}
	}
	matched := false
	answer := verdict{indicator: IndicatorNone}
	for _, entry := range append(document.Services, document.Models...) {
		if _, wanted := targeted[entry.PublicID]; !wanted {
			continue
		}
		matched = true
		indicator := hyperpingIndicator(entry.Status)
		if indicatorRank(indicator) > indicatorRank(answer.indicator) {
			answer.indicator = indicator
			answer.description = describeComponent(entry.DisplayName, strings.ToLower(entry.Status))
		}
	}
	// A document that names none of the catalog's components answers a
	// different question than the one the catalog asked.
	return answer, matched
}
