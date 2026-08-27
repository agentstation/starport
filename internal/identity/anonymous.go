package identity

// AnonymousKeyID names the identity of a request that carried no gateway API
// key. It is not a key: nothing issues it, nothing stores it, and no hash
// matches it. It exists because every path behind authentication still needs
// one name to meter, to attribute usage to, and to log, and a gateway running
// with authentication disabled has no issued key to supply one.
const AnonymousKeyID = "anonymous"

// DefaultAnonymousScopes are the scopes an unauthenticated request holds when
// the operator disabled authentication.
//
// The set is every tenant scope and never "admin". Disabling authentication
// says the port is trusted, not that every caller is the operator: the admin
// plane creates keys, applies deployment-wide provider credentials, and
// deletes accounts, and none of that should follow from opening inference.
//
// The set is a policy and not a mirror of the route table. A scope added to a
// route and not added here refuses an unauthenticated caller, which is the
// direction a default should fail in.
func DefaultAnonymousScopes() []string {
	return []string{
		"chat:write",
		"embeddings:write",
		"images:write",
		"audio:write",
		"models:read",
		"activity:read",
		"presets:write",
		"provider_keys:read",
		"provider_keys:write",
	}
}

// Anonymous returns the identity an unauthenticated request runs as. It names
// no account, so it resolves to the canonical tenant through the same rule
// every issued key uses, and it is active and unexpiring because there is
// nothing to revoke: an operator revokes it by requiring authentication again.
//
// An empty scope list yields DefaultAnonymousScopes. A caller that wants an
// unauthenticated request to hold nothing has to say so with a scope that
// grants nothing, not with silence.
func Anonymous(scopes []string) APIKey {
	granted := scopes
	if len(granted) == 0 {
		granted = DefaultAnonymousScopes()
	}
	return APIKey{
		ID:     AnonymousKeyID,
		Name:   AnonymousKeyID,
		Scopes: append([]string(nil), granted...),
		Active: true,
	}
}
