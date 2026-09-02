// Package audit owns the durable trail of admin mutations: the record, the
// actor vocabulary, and the retention window. Key creation, credential
// changes, and policy edits each leave one actor-attributed record here, so
// "who changed this and when" outlives the request that did it.
//
// A record never holds a credential value. It names the subject a mutation
// touched — a key ID, a provider, a preset name — and how the attempt ended,
// and nothing the store would have to protect.
package audit

import "time"

// Actor prefixes. An actor is one string with a kind prefix, so the trail
// reads without a join: "key:ci-deployer", "console:local-token",
// "user:auth0|5f7c…".
const (
	// ActorKeyPrefix marks an actor authenticated by a gateway API key. The
	// suffix is the key's name, or its ID when the key has no name.
	ActorKeyPrefix = "key:"
	// ActorConsolePrefix marks a machine-local console session. The suffix is
	// the grant kind that minted it.
	ActorConsolePrefix = "console:"
	// ActorUserPrefix marks an identity-provider user. The suffix is the
	// subject the provider asserted.
	ActorUserPrefix = "user:"
	// ActorAnonymous names a request that carried no identity at all, which
	// only a deployment with authentication disabled produces.
	ActorAnonymous = "anonymous"
)

// Outcomes. A record lands only when a mutation reached its store, so the
// outcome separates the write that took from the write the store refused.
const (
	// OutcomeOK reports the mutation succeeded.
	OutcomeOK = "ok"
	// OutcomeError reports the store refused or failed the mutation.
	OutcomeError = "error"
)

// Record is one admin mutation: when, who, what they did, what it touched,
// and how it ended.
type Record struct {
	// ID orders records and carries the paging cursor. The store assigns it;
	// a caller-provided value is ignored.
	ID int64 `json:"id"`
	// Time is when the mutation happened, in UTC.
	Time time.Time `json:"time"`
	// Actor is who asked, with its kind prefix.
	Actor string `json:"actor"`
	// Action names the mutation, as concept.verb: "key.create",
	// "auth_mode.update".
	Action string `json:"action"`
	// Subject is the identifier the mutation touched. Never a credential
	// value.
	Subject string `json:"subject"`
	// Outcome is OutcomeOK or OutcomeError.
	Outcome string `json:"outcome"`
	// RequestID is the gateway request that carried the mutation, so the
	// trail joins the usage listing and the request log. It is empty for a
	// write that reached the store without a request context.
	RequestID string `json:"request_id"`
}
