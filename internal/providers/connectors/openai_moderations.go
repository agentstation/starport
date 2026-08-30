package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// Moderate performs a moderation call against an OpenAI-compatible API. The
// wire answers each category twice, once as a threshold decision under
// categories and once as a score under category_scores, and both maps use
// the same names. The decode below joins them by name into one verdict per
// category, sorted by name so the same answer reads the same on every run.
func (c *OpenAICompatibleConnector) Moderate(
	ctx context.Context,
	req *ModerationRequest,
	setHeaders setHeadersFunc,
	handleError handleErrorFunc,
) (*ModerationResponse, error) {
	if len(req.Inputs) == 0 {
		return nil, fmt.Errorf("%w: moderation carries no input", ErrInvalidMediaRequest)
	}
	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOpenAI)
	if err != nil {
		return nil, err
	}
	httpReq, err := jsonRequest(ctx, endpoint, struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{
		Model: req.Model,
		Input: req.Inputs,
	})
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, httpReq, req.Credential, setHeaders, handleError)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var decoded struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Results []struct {
			Flagged        bool               `json:"flagged"`
			Categories     map[string]bool    `json:"categories"`
			CategoryScores map[string]float64 `json:"category_scores"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	// A result that answers a different number of inputs would shift every
	// later verdict onto the wrong input. It is a provider defect, and the
	// caller has to see it rather than read a confident answer built from it.
	if len(decoded.Results) != len(req.Inputs) {
		return nil, fmt.Errorf(
			"provider returned %d moderation results for %d inputs",
			len(decoded.Results), len(req.Inputs),
		)
	}
	results := make([]ModerationResult, len(decoded.Results))
	for index, result := range decoded.Results {
		results[index] = ModerationResult{
			Flagged:    result.Flagged,
			Categories: joinModerationCategories(result.Categories, result.CategoryScores),
		}
	}
	return &ModerationResponse{ID: decoded.ID, Model: decoded.Model, Results: results}, nil
}

// joinModerationCategories merges the wire's two per-category maps into one
// sorted verdict list. A category either map names appears once, because a
// score with no decision and a decision with no score are both still
// verdicts the caller should read.
func joinModerationCategories(
	flags map[string]bool,
	scores map[string]float64,
) []ModerationCategory {
	names := make(map[string]struct{}, len(scores))
	for name := range flags {
		names[name] = struct{}{}
	}
	for name := range scores {
		names[name] = struct{}{}
	}
	categories := make([]ModerationCategory, 0, len(names))
	for name := range names {
		categories = append(categories, ModerationCategory{
			Name:    name,
			Flagged: flags[name],
			Score:   scores[name],
		})
	}
	sort.Slice(categories, func(left, right int) bool {
		return categories[left].Name < categories[right].Name
	})
	return categories
}
