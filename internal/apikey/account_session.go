package apikey

// AccountSession supplies account scopes for a verified identity grant.
// It grants no deployment-admin scope and never represents a bearer credential.
func AccountSession(userID, subject, accountID string) APIKey {
	return APIKey{ID: "session:" + userID, Name: "account-session", Hash: subject, AccountID: accountID, Scopes: DefaultAnonymousScopes(), Active: true}
}
