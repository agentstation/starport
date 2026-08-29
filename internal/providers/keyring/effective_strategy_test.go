package keyring

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEffectiveStrategyNarrowsButNeverWidens pins the rule that decides who
// pays. The operator sets the account's strategy; a gateway API key may ask for
// a narrower one, so an account the operator denied every operator credential
// cannot buy one back by stamping a wider value on its own key.
func TestEffectiveStrategyNarrowsButNeverWidens(t *testing.T) {
	tests := []struct {
		name      string
		governing Strategy
		metadata  map[string]any
		want      Strategy
		wantErr   error
	}{
		{
			name:      "absent metadata inherits the account strategy",
			governing: BYOKOnly, metadata: nil, want: BYOKOnly,
		},
		{
			name:      "absent metadata inherits an operator strategy too",
			governing: OperatorFirst, metadata: map[string]any{"other": "value"},
			want: OperatorFirst,
		},
		{
			name:      "a key may narrow to its own credentials",
			governing: OperatorFirst, metadata: strategyMetadata(string(BYOKOnly)),
			want: BYOKOnly,
		},
		{
			name:      "reordering the same sources is not widening",
			governing: OperatorFirst, metadata: strategyMetadata(string(BYOKFirst)),
			want: BYOKFirst,
		},
		{
			name:      "byok_only refuses a key that asks for operator credentials",
			governing: BYOKOnly, metadata: strategyMetadata(string(OperatorFirst)),
			wantErr: ErrStrategyWidens,
		},
		{
			name:      "byok_only refuses byok_first too",
			governing: BYOKOnly, metadata: strategyMetadata(string(BYOKFirst)),
			wantErr: ErrStrategyWidens,
		},
		{
			name:      "byok_only accepts a key that restates it",
			governing: BYOKOnly, metadata: strategyMetadata(string(BYOKOnly)),
			want: BYOKOnly,
		},
		{
			name:      "an unknown strategy is refused",
			governing: OperatorFirst, metadata: strategyMetadata("global_first"),
			wantErr: ErrInvalidStrategy,
		},
		{
			name:      "a non-string strategy is refused",
			governing: OperatorFirst, metadata: map[string]any{StrategyMetadataKey: 7},
			wantErr: ErrInvalidStrategy,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := EffectiveStrategy(test.governing, test.metadata)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				assert.Empty(t, got, "a refused strategy must not resolve to a usable value")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
			assert.LessOrEqual(t, len(got.Sources()), len(test.governing.Sources()),
				"the effective strategy may never reach more sources than the account's")
		})
	}
}

// TestAccountScopeNamesTheAccount guards the AON3 move off key-scoped storage. A
// account's BYOK credentials must outlive any one gateway API key, so the scope
// names the account and nothing else.
func TestAccountScopeNamesTheAccount(t *testing.T) {
	assert.Equal(t, "account:acct-1", AccountScope("acct-1"))
	assert.NotEqual(t, GatewayScope, AccountScope("acct-1"))
	assert.Equal(t, "*", GatewayScope)
}

func strategyMetadata(value any) map[string]any {
	return map[string]any{StrategyMetadataKey: value}
}
