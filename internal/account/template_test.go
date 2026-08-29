package account

import (
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/limits"
)

// TestTemplateValidate holds the template to the account contract: a
// template that could not become a valid account is refused at the door.
func TestTemplateValidate(t *testing.T) {
	valid := Template{
		ID:                 "team-default",
		Name:               "Team default",
		CredentialStrategy: StrategyBYOKFirst,
		Limits:             &limits.Limits{Requests: &limits.RequestLimit{Limit: 60, WindowSeconds: 60}},
		BYOKPolicy:         &BYOKPolicy{Mode: BYOKSelected, Providers: []string{"groq"}},
		Access:             []ProviderAccess{{Provider: "groq", Models: []string{"groq/compound"}}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid template refused: %v", err)
	}

	cases := []struct {
		name string
		edit func(*Template)
		want error
	}{
		{"missing id", func(v *Template) { v.ID = "" }, ErrMissingID},
		{"invalid id", func(v *Template) { v.ID = "no spaces" }, ErrInvalidID},
		{"missing name", func(v *Template) { v.Name = "" }, ErrInvalidName},
		{"unknown strategy", func(v *Template) { v.CredentialStrategy = "sometimes" }, ErrInvalidCredentialStrategy},
		{"selected byok without providers", func(v *Template) {
			v.BYOKPolicy = &BYOKPolicy{Mode: BYOKSelected}
		}, ErrInvalidBYOKPolicy},
		{"duplicate access provider", func(v *Template) {
			v.Access = []ProviderAccess{{Provider: "groq"}, {Provider: "groq"}}
		}, ErrInvalidProviderAccess},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate)
			if err := candidate.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("Validate() = %v, want %v", err, test.want)
			}
		})
	}
}

// TestTemplateStampCopiesWithoutSharing is the acceptance heart of the
// concept: the stamp is a copy, so mutating the template afterward can
// never move the account, and mutating the account can never move the
// template.
func TestTemplateStampCopiesWithoutSharing(t *testing.T) {
	template := Template{
		ID:                 "org",
		Name:               "Org",
		CredentialStrategy: StrategyBYOKFirst,
		Limits:             &limits.Limits{Requests: &limits.RequestLimit{Limit: 60, WindowSeconds: 60}},
		BYOKPolicy:         &BYOKPolicy{Mode: BYOKSelected, Providers: []string{"groq"}},
		Access:             []ProviderAccess{{Provider: "groq", Models: []string{"groq/compound"}}},
	}

	target := Account{ID: "acme", Name: "Acme", Active: true}
	template.Stamp(&target)

	if target.CredentialStrategy != StrategyBYOKFirst {
		t.Fatalf("stamped strategy = %q", target.CredentialStrategy)
	}
	if target.Limits == nil || target.Limits.Requests.Limit != 60 {
		t.Fatal("stamped limits missing")
	}
	if target.BYOKPolicy == nil || target.BYOKPolicy.Mode != BYOKSelected {
		t.Fatal("stamped BYOK policy missing")
	}
	if len(target.Access) != 1 || target.Access[0].Provider != "groq" {
		t.Fatal("stamped access missing")
	}

	// Mutate the template every way it shares memory if the stamp aliased.
	template.Limits.Requests.Limit = 1
	template.BYOKPolicy.Providers[0] = "openai"
	template.Access[0].Models[0] = "other/model"

	if target.Limits.Requests.Limit != 60 {
		t.Fatal("account limits moved with the template")
	}
	if target.BYOKPolicy.Providers[0] != "groq" {
		t.Fatal("account BYOK policy moved with the template")
	}
	if target.Access[0].Models[0] != "groq/compound" {
		t.Fatal("account access moved with the template")
	}
}

// TestTemplateStampLeavesUnnamedDefaultsEmpty proves an empty template
// stamps the open defaults, not zero-value surprises.
func TestTemplateStampLeavesUnnamedDefaultsEmpty(t *testing.T) {
	target := Account{ID: "acme", Name: "Acme", Active: true}
	Template{ID: "empty", Name: "Empty"}.Stamp(&target)

	if target.Limits != nil || target.BYOKPolicy != nil || target.Access != nil {
		t.Fatal("an empty template must stamp no policy")
	}
	if target.CredentialStrategy != "" {
		t.Fatalf("an empty template must not name a strategy, got %q", target.CredentialStrategy)
	}
	if target.EffectiveCredentialStrategy() != StrategyOperatorFirst {
		t.Fatal("a stamped account without a strategy runs operator_first")
	}
}
