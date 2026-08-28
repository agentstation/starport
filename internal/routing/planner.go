package routing

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Planner deterministically converts one request and snapshot into a route plan.
type Planner struct{}

// NewPlanner creates a stateless route planner.
func NewPlanner() Planner {
	return Planner{}
}

// Plan applies hard constraints, records rejections, and orders eligible routes.
func (Planner) Plan(request Request, snapshot Snapshot) (*Plan, error) {
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	if err := validateSnapshot(snapshot); err != nil {
		return nil, err
	}

	modelRanks := requestedModelRanks(request)
	providerRanks := orderedRanks(request.Providers.Order)
	tenantModels := tenantModelSet(request.Tenant)
	tenantProviders := normalizedSet(request.Tenant.AllowedProviders)
	onlyProviders := normalizedSet(request.Providers.Only)
	ignoredProviders := normalizedSet(request.Providers.Ignore)
	requiredCapabilities := normalizedSet(request.RequiredCapabilities)
	affinityProvider := normalize(request.AffinityProvider)
	zeroPriceModels := exactSet(request.ZeroPriceModels)

	eligible := make([]rankedCandidate, 0, len(snapshot.Candidates))
	rejections := make([]Rejection, 0)
	matchedModels := make(map[string]struct{}, len(modelRanks.ranks))
	for _, candidate := range snapshot.Candidates {
		modelRank, considered := consideredModelRank(candidate.Route, modelRanks)
		if !considered {
			continue
		}
		recordMatchedModel(candidate.Route, modelRanks, matchedModels)

		if rejection, rejected := rejectCandidate(
			candidate,
			request,
			tenantModels,
			tenantProviders,
			onlyProviders,
			ignoredProviders,
			providerRanks,
			requiredCapabilities,
			zeroPriceModels,
		); rejected {
			rejections = append(rejections, rejection)
			continue
		}
		selectedRoute := candidate.Route
		if candidate.PromptCache != nil {
			selectedRoute.PromptCacheKnown = true
			selectedRoute.PromptCache = *candidate.PromptCache
		}
		if request.Operation != "" {
			selectedRoute.Operation = request.Operation
			selectedRoute.Endpoint = candidate.Endpoints[request.Operation]
		}
		selectedRoute.MaxDocuments = candidate.MaxDocuments

		providerRank := 0
		if len(providerRanks) > 0 {
			var exists bool
			providerRank, exists = providerRanks[normalize(candidate.Route.ProviderID)]
			if !exists {
				providerRank = len(providerRanks)
			}
		}
		cost, hasCost := estimatedCost(candidate.Cost, request)
		latency, hasLatency := measuredLatency(candidate.Latency)
		eligible = append(eligible, rankedCandidate{
			candidate: candidate,
			route:     selectedRoute,
			evidence: SelectionEvidence{
				ModelRank:        modelRank,
				ProviderRank:     providerRank,
				AffinityMatched:  affinityProvider != "" && normalize(candidate.Route.ProviderID) == affinityProvider,
				EstimatedCost:    cost,
				HasCost:          hasCost,
				EstimatedLatency: latency,
				HasLatency:       hasLatency,
			},
		})
	}

	rejections = append(rejections, unmatchedModelRejections(modelRanks, matchedModels, snapshot.CatalogGenerationID)...)
	sortRankedCandidates(eligible, request.Optimization, len(providerRanks) > 0)
	sort.Slice(rejections, func(left, right int) bool {
		if rejections[left].Route.ID() != rejections[right].Route.ID() {
			return rejections[left].Route.ID() < rejections[right].Route.ID()
		}
		return rejections[left].Code < rejections[right].Code
	})

	plan := &Plan{
		catalogGenerationID:  snapshot.CatalogGenerationID,
		availabilityRevision: snapshot.AvailabilityRevision,
		attempts:             make([]Attempt, 0, len(eligible)),
		rejections:           rejections,
	}
	for _, item := range eligible {
		plan.attempts = append(plan.attempts, Attempt{
			Route:    item.route,
			Evidence: item.evidence,
		})
	}
	if len(plan.attempts) == 0 {
		// The operation comes first, because it is the coarsest mismatch: a
		// model that serves other operations cannot answer this request under
		// any modality, capability, or price the caller changes.
		if rejection, found := firstRejection(rejections, RejectionMissingOperation); found {
			return plan, fmt.Errorf(
				"%w: %w: %s: %s",
				ErrNoCandidate, ErrOperationUnsupported, rejection.Route.ID(), rejection.Detail,
			)
		}
		if rejection, found := firstRejection(rejections, RejectionMissingModality); found {
			return plan, fmt.Errorf(
				"%w: %w: %s: %s",
				ErrNoCandidate, ErrModalityUnsupported, rejection.Route.ID(), rejection.Detail,
			)
		}
		return plan, fmt.Errorf("%w: %d route(s) rejected", ErrNoCandidate, len(rejections))
	}
	return plan, nil
}

type rankedCandidate struct {
	candidate Candidate
	route     Route
	evidence  SelectionEvidence
}

func validateRequest(request Request) error {
	if request.RequiredContextTokens < 0 || request.EstimatedInputTokens < 0 || request.EstimatedOutputTokens < 0 {
		return fmt.Errorf("%w: token counts must not be negative", ErrInvalidRequest)
	}
	if request.Operation != "" && !servedOperations.Contains(request.Operation) {
		return fmt.Errorf("%w: unsupported operation %q", ErrInvalidRequest, request.Operation)
	}
	return nil
}

func validateSnapshot(snapshot Snapshot) error {
	if strings.TrimSpace(snapshot.CatalogGenerationID) == "" {
		return fmt.Errorf("%w: catalog generation ID is required", ErrInvalidSnapshot)
	}
	seen := make(map[string]struct{}, len(snapshot.Candidates))
	for index, candidate := range snapshot.Candidates {
		route := candidate.Route
		if route.CatalogGenerationID != snapshot.CatalogGenerationID {
			return fmt.Errorf("%w: candidate %d uses catalog generation %q", ErrInvalidSnapshot, index, route.CatalogGenerationID)
		}
		if strings.TrimSpace(route.ModelID) == "" || strings.TrimSpace(route.ProviderID) == "" || strings.TrimSpace(route.ProviderModelID) == "" {
			return fmt.Errorf("%w: candidate %d has incomplete route identity", ErrInvalidSnapshot, index)
		}
		if _, exists := seen[route.ID()]; exists {
			return fmt.Errorf("%w: duplicate route %q", ErrInvalidSnapshot, route.ID())
		}
		seen[route.ID()] = struct{}{}
		if candidate.ContextWindow < 0 {
			return fmt.Errorf("%w: route %q has a negative context window", ErrInvalidSnapshot, route.ID())
		}
		if candidate.MaxDocuments < 0 {
			return fmt.Errorf("%w: route %q has a negative document bound", ErrInvalidSnapshot, route.ID())
		}
		if candidate.Cost != nil && (candidate.Cost.InputPerToken < 0 || candidate.Cost.OutputPerToken < 0) {
			return fmt.Errorf("%w: route %q has a negative token price", ErrInvalidSnapshot, route.ID())
		}
		if candidate.Latency != nil && *candidate.Latency < 0 {
			return fmt.Errorf("%w: route %q has negative latency", ErrInvalidSnapshot, route.ID())
		}
		seenOperations := make(map[Operation]struct{}, len(candidate.Operations))
		for _, operation := range candidate.Operations {
			if _, exists := seenOperations[operation]; exists {
				return fmt.Errorf("%w: route %q duplicates operation %q", ErrInvalidSnapshot, route.ID(), operation)
			}
			seenOperations[operation] = struct{}{}
			if !servedOperations.Contains(operation) {
				// Catalog facts arrive from a generation this build did not
				// ship with. An operation the build cannot plan is inert, and
				// the planner keeps every other route in the snapshot. Caller
				// input still fails closed in validateRequest.
				continue
			}
			if endpoint, exists := candidate.Endpoints[operation]; !exists || strings.TrimSpace(endpoint.Protocol) == "" || strings.TrimSpace(endpoint.URL) == "" {
				return fmt.Errorf("%w: route %q has no endpoint for operation %q", ErrInvalidSnapshot, route.ID(), operation)
			}
		}
	}
	return nil
}

type modelSelection struct {
	ranks        map[string]int
	anyModelRank int
	allowAny     bool
}

func requestedModelRanks(request Request) modelSelection {
	models := append([]string(nil), request.Models...)
	for index, modelID := range models {
		if override, exists := request.Tenant.ModelOverrides[modelID]; exists && override != "" {
			models[index] = override
		}
	}
	if len(models) > 1 && !request.AllowModelFallbacks {
		models = models[:1]
	}
	selection := modelSelection{
		ranks:        make(map[string]int, len(models)),
		anyModelRank: len(models),
		allowAny:     request.AllowAnyModelFallback || len(models) == 0,
	}
	for index, modelID := range models {
		if _, exists := selection.ranks[modelID]; !exists {
			selection.ranks[modelID] = index
		}
	}
	return selection
}

func consideredModelRank(route Route, selection modelSelection) (int, bool) {
	if rank, exists := selection.ranks[route.ID()]; exists {
		return rank, true
	}
	if rank, exists := selection.ranks[route.ModelID]; exists {
		return rank, true
	}
	return selection.anyModelRank, selection.allowAny
}

// recordMatchedModel marks the requested model identities one considered
// candidate satisfied, so requested models that match nothing are reportable.
func recordMatchedModel(route Route, selection modelSelection, matched map[string]struct{}) {
	if _, exists := selection.ranks[route.ID()]; exists {
		matched[route.ID()] = struct{}{}
	}
	if _, exists := selection.ranks[route.ModelID]; exists {
		matched[route.ModelID] = struct{}{}
	}
}

// unmatchedModelRejections produces one model-level rejection per requested
// model that matched no candidate at all, so a plan never fails with zero
// recorded reasons.
func unmatchedModelRejections(
	selection modelSelection,
	matched map[string]struct{},
	catalogGenerationID string,
) []Rejection {
	unmatched := make([]string, 0)
	for modelID := range selection.ranks {
		if _, exists := matched[modelID]; !exists {
			unmatched = append(unmatched, modelID)
		}
	}
	sort.Strings(unmatched)
	rejections := make([]Rejection, 0, len(unmatched))
	for _, modelID := range unmatched {
		rejections = append(rejections, Rejection{
			Route: Route{
				CatalogGenerationID: catalogGenerationID,
				ModelID:             modelID,
			},
			Code:   RejectionUnknownModel,
			Detail: "no catalog offering matches the requested model",
		})
	}
	return rejections
}

func rejectCandidate(
	candidate Candidate,
	request Request,
	tenantModels map[string]struct{},
	tenantProviders map[string]struct{},
	onlyProviders map[string]struct{},
	ignoredProviders map[string]struct{},
	providerRanks map[string]int,
	requiredCapabilities map[string]struct{},
	zeroPriceModels map[string]struct{},
) (Rejection, bool) {
	reject := func(code RejectionCode, detail string) (Rejection, bool) {
		return Rejection{Route: candidate.Route, Code: code, Detail: detail}, true
	}
	if candidate.Unavailable {
		return reject(RejectionUnavailable, "runtime availability disabled the offering")
	}
	if candidate.Unhealthy {
		return reject(RejectionUnhealthy, "runtime health disabled the offering")
	}
	if !candidate.ServesOperation(request.Operation) {
		return reject(RejectionMissingOperation, "offering does not serve the "+string(request.Operation)+" operation")
	}
	if request.Operation != "" {
		endpoint, exists := candidate.Endpoints[request.Operation]
		if !exists || strings.TrimSpace(endpoint.Protocol) == "" || strings.TrimSpace(endpoint.URL) == "" {
			return reject(RejectionMissingEndpoint, "offering has no usable operation endpoint")
		}
	}
	if !modelAllowed(candidate.Route, tenantModels) {
		return reject(RejectionTenantModel, "tenant policy denied the model")
	}
	providerID := normalize(candidate.Route.ProviderID)
	if !setAllows(providerID, tenantProviders) {
		return reject(RejectionTenantProvider, "tenant policy denied the provider")
	}
	if !setAllows(providerID, onlyProviders) {
		return reject(RejectionProviderPolicy, "provider is not in the only list")
	}
	if _, ignored := ignoredProviders[providerID]; ignored {
		return reject(RejectionProviderPolicy, "provider is in the ignore list")
	}
	if len(providerRanks) > 0 && !request.Providers.AllowFallbacks {
		if _, ordered := providerRanks[providerID]; !ordered {
			return reject(RejectionProviderPolicy, "provider is outside the ordered set")
		}
	}
	if missing := firstMissingCapability(candidate.Capabilities, requiredCapabilities); missing != "" {
		return reject(RejectionMissingCapability, "missing capability "+missing)
	}
	if missing := firstMissingModality(candidate.InputModalities, request.RequiredModalities); missing != "" {
		return reject(RejectionMissingModality, "model does not read "+string(missing)+" input")
	}
	if request.RequiredContextTokens > 0 && candidate.ContextWindow < request.RequiredContextTokens {
		return reject(RejectionInsufficientContext, "context window is smaller than the request")
	}
	if code, detail, rejected := rejectPrice(candidate, request.Providers, zeroPriceModels); rejected {
		return reject(code, detail)
	}
	return Rejection{}, false
}

// rejectPrice enforces the request price cap and the zero-price (":free")
// model constraint. Both require a known price: a price promise cannot be
// kept for an offering whose price the catalog does not state.
func rejectPrice(
	candidate Candidate,
	policy ProviderPolicy,
	zeroPriceModels map[string]struct{},
) (RejectionCode, string, bool) {
	_, zeroByModel := zeroPriceModels[candidate.Route.ModelID]
	_, zeroByRoute := zeroPriceModels[candidate.Route.ID()]
	requireZero := zeroByModel || zeroByRoute
	capped := policy.MaxPromptPricePerToken > 0 || policy.MaxCompletionPricePerToken > 0
	if !requireZero && !capped {
		return "", "", false
	}
	if candidate.Cost == nil {
		return RejectionPriceExceeded, "offering price is unknown", true
	}
	if requireZero && (candidate.Cost.InputPerToken > 0 || candidate.Cost.OutputPerToken > 0) {
		return RejectionPriceExceeded, "the :free variant requires a zero-price offering", true
	}
	if policy.MaxPromptPricePerToken > 0 && candidate.Cost.InputPerToken > policy.MaxPromptPricePerToken {
		return RejectionPriceExceeded, "prompt price exceeds the request max_price", true
	}
	if policy.MaxCompletionPricePerToken > 0 && candidate.Cost.OutputPerToken > policy.MaxCompletionPricePerToken {
		return RejectionPriceExceeded, "completion price exceeds the request max_price", true
	}
	return "", "", false
}

func modelAllowed(route Route, allowed map[string]struct{}) bool {
	if len(allowed) == 0 {
		return true
	}
	if _, exists := allowed["*"]; exists {
		return true
	}
	if _, exists := allowed[route.ModelID]; exists {
		return true
	}
	_, exists := allowed[route.ID()]
	return exists
}

func firstMissingCapability(capabilities []string, required map[string]struct{}) string {
	if len(required) == 0 {
		return ""
	}
	provided := normalizedSet(capabilities)
	missing := make([]string, 0)
	for capability := range required {
		if _, exists := provided[capability]; !exists {
			missing = append(missing, capability)
		}
	}
	sort.Strings(missing)
	if len(missing) == 0 {
		return ""
	}
	return missing[0]
}

// firstMissingModality names the first requested modality a route does not
// read. An empty accepted list means the catalog states no input modalities
// for that model, and silence is not a refusal: rejecting on silence would
// drop every model the catalog has not described yet.
func firstMissingModality(accepted, required []Modality) Modality {
	if len(required) == 0 || len(accepted) == 0 {
		return ""
	}
	for _, modality := range required {
		found := false
		for _, candidate := range accepted {
			if candidate == modality {
				found = true
				break
			}
		}
		if !found {
			return modality
		}
	}
	return ""
}

// firstRejection returns the first rejection carrying one code. The caller
// sorts rejections before reading them, so the answer is stable across runs
// over the same snapshot.
func firstRejection(rejections []Rejection, code RejectionCode) (Rejection, bool) {
	for _, rejection := range rejections {
		if rejection.Code == code {
			return rejection, true
		}
	}
	return Rejection{}, false
}

func sortRankedCandidates(items []rankedCandidate, optimization OptimizationPolicy, hasProviderOrder bool) {
	sort.Slice(items, func(left, right int) bool {
		leftEvidence := items[left].evidence
		rightEvidence := items[right].evidence
		if leftEvidence.ModelRank != rightEvidence.ModelRank {
			return leftEvidence.ModelRank < rightEvidence.ModelRank
		}
		if hasProviderOrder && leftEvidence.ProviderRank != rightEvidence.ProviderRank {
			return leftEvidence.ProviderRank < rightEvidence.ProviderRank
		}
		if leftEvidence.AffinityMatched != rightEvidence.AffinityMatched {
			return leftEvidence.AffinityMatched
		}
		if optimization.PreferLowestCost {
			if leftEvidence.HasCost != rightEvidence.HasCost {
				return leftEvidence.HasCost
			}
			if leftEvidence.HasCost && leftEvidence.EstimatedCost != rightEvidence.EstimatedCost {
				return leftEvidence.EstimatedCost < rightEvidence.EstimatedCost
			}
		}
		if optimization.PreferLowestLatency {
			if leftEvidence.HasLatency != rightEvidence.HasLatency {
				return leftEvidence.HasLatency
			}
			if leftEvidence.HasLatency && leftEvidence.EstimatedLatency != rightEvidence.EstimatedLatency {
				return leftEvidence.EstimatedLatency < rightEvidence.EstimatedLatency
			}
		}
		return items[left].candidate.Route.ID() < items[right].candidate.Route.ID()
	})
}

func estimatedCost(cost *TokenCost, request Request) (float64, bool) {
	if cost == nil {
		return 0, false
	}
	return cost.InputPerToken*float64(request.EstimatedInputTokens) +
		cost.OutputPerToken*float64(request.EstimatedOutputTokens), true
}

func measuredLatency(latency *time.Duration) (time.Duration, bool) {
	if latency == nil {
		return 0, false
	}
	return *latency, true
}

func orderedRanks(values []string) map[string]int {
	ranks := make(map[string]int, len(values))
	nextRank := 0
	for _, value := range values {
		value = normalize(value)
		if value == "" {
			continue
		}
		if _, exists := ranks[value]; !exists {
			ranks[value] = nextRank
			nextRank++
		}
	}
	return ranks
}

func exactSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func tenantModelSet(policy TenantPolicy) map[string]struct{} {
	allowed := exactSet(policy.AllowedModels)
	for source, target := range policy.ModelOverrides {
		if target == "" {
			continue
		}
		if len(allowed) == 0 {
			continue
		}
		if _, all := allowed["*"]; all {
			continue
		}
		if _, sourceAllowed := allowed[source]; sourceAllowed {
			allowed[target] = struct{}{}
		}
	}
	return allowed
}

func normalizedSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := normalize(value); normalized != "" {
			result[normalized] = struct{}{}
		}
	}
	return result
}

func setAllows(value string, allowed map[string]struct{}) bool {
	if len(allowed) == 0 {
		return true
	}
	if _, exists := allowed["*"]; exists {
		return true
	}
	_, exists := allowed[value]
	return exists
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
