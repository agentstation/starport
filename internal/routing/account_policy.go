package routing

// AllowsRoute checks model and provider grants without runtime availability.
func (p AccountPolicy) AllowsRoute(route Route) bool {
	code, _ := accountRejection(route, accountModelSet(p), normalizedSet(p.AllowedProviders), accessIndex(p.Access))
	return code == ""
}

func accountRejection(route Route, models, providers map[string]struct{}, access map[string]map[string]struct{}) (RejectionCode, string) {
	if !modelAllowed(route, models) {
		return RejectionAccountModel, "account policy denied the model"
	}
	provider := normalize(route.ProviderID)
	if !setAllows(provider, providers) {
		return RejectionAccountProvider, "account policy denied the provider"
	}
	if len(access) > 0 {
		grantedModels, granted := access[provider]
		if !granted {
			return RejectionAccountProvider, "account access does not grant the provider"
		}
		if grantedModels != nil && !modelAllowed(route, grantedModels) {
			return RejectionAccountModel, "account access does not grant the model on this provider"
		}
	}
	return "", ""
}
