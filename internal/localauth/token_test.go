package localauth

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMintProducesADistinctCredentialEachTime is the property a rotation
// depends on. A generator that repeated itself would make rotation theatre.
func TestMintProducesADistinctCredentialEachTime(t *testing.T) {
	now := time.Now()
	seen := make(map[string]struct{}, 64)
	for range 64 {
		token, err := Mint(1, now)
		require.NoError(t, err)
		require.NoError(t, token.Validate())
		_, repeated := seen[token.Secret]
		require.False(t, repeated, "Mint repeated a secret")
		seen[token.Secret] = struct{}{}
	}
}

// TestTokenIsNotMistakableForAGatewayKey keeps the two credentials apart where
// a person reads them. They authorize different things and are revoked in
// different ways, so a prefix that told them apart only in documentation would
// not be telling them apart.
func TestTokenIsNotMistakableForAGatewayKey(t *testing.T) {
	token, err := Mint(1, time.Now())
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(token.Secret, TokenPrefix))
	assert.False(t, strings.HasPrefix(token.Secret, "STARPORT_"))
	assert.Greater(t, len(token.Secret), len(TokenPrefix)+32)
}

func TestMintRefusesGenerationZero(t *testing.T) {
	_, err := Mint(0, time.Now())
	assert.ErrorIs(t, err, ErrCorruptRecord)
}

// TestAuthorizesMatchesExactly guards the comparison. A prefix or a
// case-folding match would accept a truncated secret, and an empty-accepts-empty
// match would let a caller who presents nothing in.
func TestAuthorizesMatchesExactly(t *testing.T) {
	token, err := Mint(1, time.Now())
	require.NoError(t, err)

	assert.True(t, token.Authorizes(token.Secret))
	assert.False(t, token.Authorizes(token.Secret[:len(token.Secret)-1]))
	assert.False(t, token.Authorizes(token.Secret+"x"))
	assert.False(t, token.Authorizes(strings.ToUpper(token.Secret)))
	assert.False(t, token.Authorizes(""))
	assert.False(t, Token{}.Authorizes(""), "an absent token authorizes nobody")
}

// TestRedactedDropsTheSecret covers everything that reports on the token rather
// than presenting it: `auth status`, a log line, a JSON view.
func TestRedactedDropsTheSecret(t *testing.T) {
	token, err := Mint(3, time.Now())
	require.NoError(t, err)

	redacted := token.Redacted()

	assert.Empty(t, redacted.Secret)
	assert.Equal(t, uint64(3), redacted.Generation)
	assert.NotEmpty(t, token.Secret, "redaction must not mutate the caller's token")
}

// TestAllowsExposure is the AON8 tripwire. It has the same shape as the AON6
// authentication tripwire and reuses its loopback rule, so a change to what
// counts as this machine changes both at once.
func TestAllowsExposure(t *testing.T) {
	rotatedAt := time.Now().UTC()
	fresh := Token{RotatedAt: &rotatedAt}
	firstBoot := Token{}

	tests := []struct {
		name  string
		host  string
		token Token
		want  bool
	}{
		{name: "loopback with a first-boot token", host: "127.0.0.1", token: firstBoot, want: true},
		{name: "localhost with a first-boot token", host: "localhost", token: firstBoot, want: true},
		{name: "every interface with a first-boot token", host: "0.0.0.0", token: firstBoot, want: false},
		{name: "an empty host with a first-boot token", host: "", token: firstBoot, want: false},
		{name: "a routable address with a first-boot token", host: "10.0.0.4", token: firstBoot, want: false},
		{name: "every interface with a rotated token", host: "0.0.0.0", token: fresh, want: true},
		{name: "a routable address with a rotated token", host: "10.0.0.4", token: fresh, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, AllowsExposure(test.host, test.token))
		})
	}
}

// TestRotatedRefusesAZeroTimestamp covers the record a hand-edited file can
// produce. An empty RFC 3339 string decodes to the zero time, and reading that
// as "rotated" would lift the exposure refusal for free.
func TestRotatedRefusesAZeroTimestamp(t *testing.T) {
	var zero time.Time
	assert.False(t, Token{RotatedAt: &zero}.Rotated())
	assert.False(t, Token{}.Rotated())
}

func TestValidateNamesWhatIsWrong(t *testing.T) {
	valid, err := Mint(2, time.Now())
	require.NoError(t, err)
	require.NoError(t, valid.Validate())

	missingIssue := valid
	missingIssue.IssuedAt = time.Time{}
	assert.ErrorIs(t, missingIssue.Validate(), ErrCorruptRecord)

	shortSecret := valid
	shortSecret.Secret = TokenPrefix
	assert.ErrorIs(t, shortSecret.Validate(), ErrCorruptRecord)

	olderVersion := valid
	olderVersion.Version = 0
	assert.ErrorIs(t, olderVersion.Validate(), ErrUnsupportedVersion)
}
