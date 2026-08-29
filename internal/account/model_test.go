package account_test

import (
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
)

func validAccount() account.Account {
	now := time.Now().UTC()
	return account.Account{
		ID:        "acme",
		Name:      "Acme",
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestValidateBYOKPolicy(t *testing.T) {
	cases := []struct {
		name   string
		policy *account.BYOKPolicy
		want   error
	}{
		{name: "nil policy allows everything", policy: nil, want: nil},
		{name: "all", policy: &account.BYOKPolicy{Mode: account.BYOKAll}, want: nil},
		{name: "none", policy: &account.BYOKPolicy{Mode: account.BYOKNone}, want: nil},
		{
			name:   "selected with providers",
			policy: &account.BYOKPolicy{Mode: account.BYOKSelected, Providers: []string{"openai"}},
			want:   nil,
		},
		{
			name:   "selected without providers",
			policy: &account.BYOKPolicy{Mode: account.BYOKSelected},
			want:   account.ErrInvalidBYOKPolicy,
		},
		{
			name:   "selected with empty provider",
			policy: &account.BYOKPolicy{Mode: account.BYOKSelected, Providers: []string{""}},
			want:   account.ErrInvalidBYOKPolicy,
		},
		{
			name:   "all with providers",
			policy: &account.BYOKPolicy{Mode: account.BYOKAll, Providers: []string{"openai"}},
			want:   account.ErrInvalidBYOKPolicy,
		},
		{
			name:   "none with providers",
			policy: &account.BYOKPolicy{Mode: account.BYOKNone, Providers: []string{"openai"}},
			want:   account.ErrInvalidBYOKPolicy,
		},
		{
			name:   "unknown mode",
			policy: &account.BYOKPolicy{Mode: account.BYOKMode("some")},
			want:   account.ErrInvalidBYOKPolicy,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := validAccount()
			record.BYOKPolicy = tc.policy
			err := record.Validate()
			if !errors.Is(err, tc.want) && !(tc.want == nil && err == nil) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateProviderAccess(t *testing.T) {
	cases := []struct {
		name   string
		access []account.ProviderAccess
		want   error
	}{
		{name: "nil access allows everything", access: nil, want: nil},
		{
			name:   "providers with and without models",
			access: []account.ProviderAccess{{Provider: "openai"}, {Provider: "groq", Models: []string{"llama-3.3-70b"}}},
			want:   nil,
		},
		{
			name:   "empty provider",
			access: []account.ProviderAccess{{Provider: ""}},
			want:   account.ErrInvalidProviderAccess,
		},
		{
			name:   "duplicate provider",
			access: []account.ProviderAccess{{Provider: "openai"}, {Provider: "openai"}},
			want:   account.ErrInvalidProviderAccess,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := validAccount()
			record.Access = tc.access
			err := record.Validate()
			if !errors.Is(err, tc.want) && !(tc.want == nil && err == nil) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAllowsBYOK(t *testing.T) {
	record := validAccount()
	if !record.AllowsBYOK("openai") {
		t.Fatal("nil BYOKPolicy must allow every provider")
	}

	record.BYOKPolicy = &account.BYOKPolicy{Mode: account.BYOKAll}
	if !record.AllowsBYOK("anthropic") {
		t.Fatal("BYOKAll must allow every provider")
	}

	record.BYOKPolicy = &account.BYOKPolicy{Mode: account.BYOKNone}
	if record.AllowsBYOK("openai") {
		t.Fatal("BYOKNone must refuse every provider")
	}

	record.BYOKPolicy = &account.BYOKPolicy{
		Mode:      account.BYOKSelected,
		Providers: []string{"openai"},
	}
	if !record.AllowsBYOK("openai") {
		t.Fatal("BYOKSelected must allow a named provider")
	}
	if record.AllowsBYOK("anthropic") {
		t.Fatal("BYOKSelected must refuse a provider outside the set")
	}
}

func TestAllowsProviderAndModel(t *testing.T) {
	record := validAccount()
	if !record.AllowsProvider("openai") || !record.AllowsModel("openai", "gpt-4o") {
		t.Fatal("an account without access entries must reach every provider and model")
	}

	record.Access = []account.ProviderAccess{
		{Provider: "openai"},
		{Provider: "groq", Models: []string{"llama-3.3-70b"}},
	}

	if !record.AllowsProvider("openai") {
		t.Fatal("a listed provider must be reachable")
	}
	if record.AllowsProvider("anthropic") {
		t.Fatal("an unlisted provider must be refused")
	}
	if !record.AllowsModel("openai", "gpt-4o") {
		t.Fatal("an entry without models must grant every model of its provider")
	}
	if !record.AllowsModel("groq", "llama-3.3-70b") {
		t.Fatal("a listed model must be reachable")
	}
	if record.AllowsModel("groq", "mixtral-8x7b") {
		t.Fatal("a model outside a narrowed entry must be refused")
	}
	if record.AllowsModel("anthropic", "claude-sonnet-5") {
		t.Fatal("a model on an unlisted provider must be refused")
	}
}
