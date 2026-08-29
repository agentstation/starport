package identity

// LocalOperatorKeyID names the identity of a request that carried a console
// session rather than a gateway API key. Like AnonymousKeyID it is not a key:
// nothing issues it, nothing stores it, and no hash matches it. It exists
// because usage, rate limits, and logs all need one name for the caller, and a
// session names a machine rather than an issued credential.
const LocalOperatorKeyID = "local-operator"

// LocalOperator returns the identity a console session runs as.
//
// It holds the wildcard scope, which the anonymous identity deliberately does
// not. The two are not comparable: disabling authentication says the port is
// trusted, and anyone who can reach the port arrives as anonymous. A session is
// held only by a browser that redeemed a ticket signed with a file this machine
// keeps at mode 0600, so its holder is an account on this machine — the same
// account that can run the CLI, read the database, and edit the configuration.
// Granting it less than everything would not withhold any power; it would only
// send an operator to a terminal to do what the console refused.
//
// It names no account, so it resolves to the canonical account through the same
// rule every issued key uses. An operator who wants a session metered against a
// different account changes what that rule returns rather than what a cookie
// claims.
func LocalOperator() APIKey {
	return APIKey{
		ID:     LocalOperatorKeyID,
		Name:   LocalOperatorKeyID,
		Scopes: []string{"*"},
		Active: true,
	}
}
