package limits

// A tenant limit and a key limit are not two candidate values for one meter.
// They meter different populations: the account meter counts every key the
// account holds, and the key meter counts one key. Resolving them to whichever
// number is smaller would let an account with N keys spend N times its own cap,
// because each key would stay inside the smaller value on its own meter.
//
// So a request satisfies every rule that applies to it. This file owns that
// rule so no enforcement path re-derives it, and each rule carries the scope
// that set it so a refusal can name the owner an operator has to talk to.

// Scope names the holder that set a limit.
type Scope string

const (
	// ScopeTenant is an account-wide limit. It meters every key the account
	// holds, so it is the operator's cap on what the account may spend.
	ScopeTenant Scope = "tenant"
	// ScopeKey is one gateway API key's own limit.
	ScopeKey Scope = "key"
)

// Dimension names which consumption budget a rule meters.
type Dimension string

const (
	// DimensionSpend meters integer nano-USD spend.
	DimensionSpend Dimension = "spend"
	// DimensionTokens meters total token consumption.
	DimensionTokens Dimension = "token"
)

// RequestRule is one request-rate meter a request must satisfy.
type RequestRule struct {
	Scope Scope
	Limit RequestLimit
}

// BudgetRule is one consumption meter a request must satisfy.
type BudgetRule struct {
	Scope  Scope
	Budget Budget
}

// RequestRules returns every request-rate meter one request must satisfy,
// account before key. deploymentDefault is the gateway's global window; it
// applies at key scope only when the key sets no request limit of its own,
// because an explicit key limit is admin intent about that key. Pass nil when
// the deployment has no global window.
func RequestRules(tenantLimits, keyLimits *Limits, deploymentDefault *RequestLimit) []RequestRule {
	rules := make([]RequestRule, 0, 2)
	if tenantLimits != nil && tenantLimits.Requests != nil {
		rules = append(rules, RequestRule{Scope: ScopeTenant, Limit: *tenantLimits.Requests})
	}
	switch {
	case keyLimits != nil && keyLimits.Requests != nil:
		rules = append(rules, RequestRule{Scope: ScopeKey, Limit: *keyLimits.Requests})
	case deploymentDefault != nil:
		rules = append(rules, RequestRule{Scope: ScopeKey, Limit: *deploymentDefault})
	}
	return rules
}

// BudgetRules returns every consumption meter one request must satisfy for one
// dimension, account before key.
func BudgetRules(tenantLimits, keyLimits *Limits, dimension Dimension) []BudgetRule {
	rules := make([]BudgetRule, 0, 2)
	for _, holder := range []struct {
		scope  Scope
		limits *Limits
	}{
		{ScopeTenant, tenantLimits},
		{ScopeKey, keyLimits},
	} {
		if budget := holder.limits.budget(dimension); budget != nil {
			rules = append(rules, BudgetRule{Scope: holder.scope, Budget: *budget})
		}
	}
	return rules
}

// budget selects one dimension's budget, reporting nil for an unset dimension,
// an unset holder, and an unknown dimension alike: none of the three bounds
// the request.
func (l *Limits) budget(dimension Dimension) *Budget {
	if l == nil {
		return nil
	}
	switch dimension {
	case DimensionSpend:
		return l.Spend
	case DimensionTokens:
		return l.Tokens
	}
	return nil
}

// StoredBytesRule is one stored byte bound an upload must satisfy.
type StoredBytesRule struct {
	Scope Scope
	Limit int64
}

// TightestStoredBytes reports the stored byte bound an upload must satisfy,
// and whether one applies at all.
//
// Stored bytes resolve to the smaller of the two, which is the opposite of
// what RequestRules does above, and for a reason the shapes of the two meters
// give. A request rate meters two different populations, so both meters have
// to run. Stored bytes meter one: a file belongs to an account, and a key
// holds no bytes of its own. Both bounds therefore read the same counter, and
// running the smaller one satisfies the larger by arithmetic.
//
// A key bound is an operator asking that this key not push the account past a
// tighter number than the account's own. The returned scope names which holder
// set the bound, so a refusal can name the owner an operator has to talk to.
func TightestStoredBytes(tenantLimits, keyLimits *Limits) (StoredBytesRule, bool) {
	var tightest StoredBytesRule
	found := false
	for _, holder := range []struct {
		scope  Scope
		limits *Limits
	}{
		{ScopeTenant, tenantLimits},
		{ScopeKey, keyLimits},
	} {
		if holder.limits == nil || holder.limits.StoredBytes == nil {
			continue
		}
		bound := *holder.limits.StoredBytes
		if !found || bound < tightest.Limit {
			tightest = StoredBytesRule{Scope: holder.scope, Limit: bound}
			found = true
		}
	}
	return tightest, found
}
