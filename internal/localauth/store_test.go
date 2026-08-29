package localauth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tokenPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "auth", "local-admin-token.json")
}

func newStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := NewStore(path)
	require.NoError(t, err)
	return store
}

// TestConcurrentStartsMintOneToken is the acceptance case. Two gateways
// starting at once on a cold data directory must end up holding the same
// credential: if each minted its own, the second would overwrite the first and
// the operator would be holding a token the running process does not accept.
func TestConcurrentStartsMintOneToken(t *testing.T) {
	path := tokenPath(t)
	const starts = 8

	var wait sync.WaitGroup
	secrets := make([]string, starts)
	mints := make([]bool, starts)
	failures := make([]error, starts)
	begin := make(chan struct{})
	for index := range starts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			// Each start opens the file itself, which is what a second process
			// does. Sharing one Store would test nothing.
			store, err := NewStore(path)
			if err != nil {
				failures[index] = err
				return
			}
			<-begin
			token, minted, err := store.LoadOrMint(context.Background(), time.Now())
			secrets[index], mints[index], failures[index] = token.Secret, minted, err
		}()
	}
	close(begin)
	wait.Wait()

	minted := 0
	for index := range starts {
		require.NoError(t, failures[index])
		assert.Equal(t, secrets[0], secrets[index], "every start has to hold the same token")
		if mints[index] {
			minted++
		}
	}
	assert.Equal(t, 1, minted, "exactly one start may report that it minted the token")
	assert.NotEmpty(t, secrets[0])
}

// TestTokenFileIsOwnerOnly pins the mode. The token is a claim about which
// account you are on this machine, and any wider mode turns it into a claim
// about which machine you are on.
func TestTokenFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not carry Unix file modes")
	}
	path := tokenPath(t)
	store := newStore(t, path)

	_, _, err := store.LoadOrMint(context.Background(), time.Now())
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// A rotation replaces an existing file, and a rename carries the
	// destination's mode rather than the source's, so the mode is worth proving
	// on the second write as well as the first.
	require.NoError(t, os.Chmod(path, 0o644))
	_, err = store.Rotate(context.Background(), time.Now())
	require.NoError(t, err)
	rotatedInfo, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), rotatedInfo.Mode().Perm())
}

// TestRotateReplacesTheSecretAndSaysSo covers what an operator checks after
// running the command: a different secret, a higher generation, and a rotation
// time that lifts the exposure refusal.
func TestRotateReplacesTheSecretAndSaysSo(t *testing.T) {
	store := newStore(t, tokenPath(t))
	first, _, err := store.LoadOrMint(context.Background(), time.Now())
	require.NoError(t, err)
	require.False(t, first.Rotated(), "a first-boot token has not been rotated")

	rotatedAt := time.Now().UTC().Truncate(time.Second)
	rotated, err := store.Rotate(context.Background(), rotatedAt)
	require.NoError(t, err)

	assert.NotEqual(t, first.Secret, rotated.Secret)
	assert.Equal(t, first.Generation+1, rotated.Generation)
	require.True(t, rotated.Rotated())
	assert.Equal(t, rotatedAt, rotated.RotatedAt.UTC())

	// What a later start reads has to be what the rotation wrote, or the
	// operator is holding a secret the running gateway does not accept.
	reloaded, err := store.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, rotated, reloaded)

	again, err := store.Rotate(context.Background(), time.Now())
	require.NoError(t, err)
	assert.Equal(t, uint64(3), again.Generation, "the generation counts rotations, it does not reset")
}

// TestRotateWorksOnAColdMachine matters because the message that refuses a
// public bind names `starport auth rotate`. An operator who follows that
// instruction before ever starting the gateway has to get a token, not an
// error telling them to start it first.
func TestRotateWorksOnAColdMachine(t *testing.T) {
	store := newStore(t, tokenPath(t))

	rotated, err := store.Rotate(context.Background(), time.Now())

	require.NoError(t, err)
	assert.Equal(t, uint64(1), rotated.Generation)
	assert.True(t, rotated.Rotated())
	assert.True(t, AllowsExposure("0.0.0.0", rotated))
}

// TestLoadReportsAColdMachineAsNotFound keeps "nothing has been minted" a state
// a caller can act on. A command that has to distinguish "no token yet" from
// "the disk is broken" cannot do it from a single error.
func TestLoadReportsAColdMachineAsNotFound(t *testing.T) {
	store := newStore(t, tokenPath(t))

	_, err := store.Load(context.Background())

	assert.ErrorIs(t, err, ErrNotFound)
}

// TestUnreadableRecordsAreRefusedRatherThanReplaced is the destructive case.
// Minting over a file that failed to decode would throw away the credential an
// operator is currently holding, and they would find out when the token they
// have stops working.
func TestUnreadableRecordsAreRefusedRatherThanReplaced(t *testing.T) {
	valid, err := Mint(4, time.Now())
	require.NoError(t, err)
	newerVersion := valid
	newerVersion.Version = TokenVersion + 1
	wrongScope := valid
	wrongScope.Scope = "account-admin"
	notAToken := valid
	notAToken.Secret = "STARPORT_looks_like_a_gateway_key"

	tests := []struct {
		name    string
		content []byte
		wantErr error
	}{
		{name: "not JSON", content: []byte("{"), wantErr: ErrCorruptRecord},
		{name: "a newer layout", content: encode(t, newerVersion), wantErr: ErrUnsupportedVersion},
		{name: "another scope", content: encode(t, wrongScope), wantErr: ErrCorruptRecord},
		{name: "a gateway key", content: encode(t, notAToken), wantErr: ErrCorruptRecord},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := tokenPath(t)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, test.content, 0o600))
			store := newStore(t, path)

			_, _, err := store.LoadOrMint(context.Background(), time.Now())

			require.ErrorIs(t, err, test.wantErr)
			after, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, test.content, after, "the unreadable file must survive untouched")
		})
	}
}

// TestRotateRepairsAnUnreadableRecord is the deliberate exception to the rule
// above. Refusing to read is the safe answer while the operator still needs the
// old secret; refusing to rotate would leave a machine with a broken file and
// no command that fixes it.
func TestRotateRepairsAnUnreadableRecord(t *testing.T) {
	path := tokenPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	store := newStore(t, path)

	rotated, err := store.Rotate(context.Background(), time.Now())

	require.NoError(t, err)
	assert.Equal(t, uint64(1), rotated.Generation)
	reloaded, err := store.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, rotated.Secret, reloaded.Secret)
}

// TestWritesLeaveNoStraySecrets covers the failure nobody looks for: a
// temporary file holding a real token left behind in the data directory.
func TestWritesLeaveNoStraySecrets(t *testing.T) {
	path := tokenPath(t)
	store := newStore(t, path)
	_, _, err := store.LoadOrMint(context.Background(), time.Now())
	require.NoError(t, err)
	_, err = store.Rotate(context.Background(), time.Now())
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	for _, name := range names {
		assert.False(t, strings.HasPrefix(name, ".local-admin-token-"),
			"a temporary token file survived: %v", names)
	}
}

func TestNewStoreRequiresAnAbsolutePath(t *testing.T) {
	_, err := NewStore("")
	assert.ErrorIs(t, err, ErrPathRequired)

	// A relative path resolves against whatever directory the process happens
	// to be in, which for a credential is a different file per invocation.
	_, err = NewStore("local-admin-token.json")
	assert.ErrorIs(t, err, ErrPathRequired)
}

func encode(t *testing.T, token Token) []byte {
	t.Helper()
	raw, err := json.Marshal(token)
	require.NoError(t, err)
	return raw
}
