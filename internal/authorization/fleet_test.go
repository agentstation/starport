package authorization

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs/permission/hostclock"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestFleetDurableReceiptPrecedesReplicaEnforcement(t *testing.T) {
	if os.Getenv("STARPORT_TEST_AUTH_REPLICA") == "1" {
		runAuthorizationReplica(t)
		return
	}
	address := os.Getenv("TEST_AUTHORIZATION_POSTGRES_URL")
	if address == "" || os.Getenv("TEST_VALKEY_URL") == "" {
		t.Skip("UNVERIFIED: Valkey and PostgreSQL are required for separate-process authorization")
	}
	for _, owner := range []string{"kv", "sql"} {
		t.Run(owner, func(t *testing.T) {
			namespace := "auth_fleet_" + strings.ToLower(rand.Text())
			admin, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: address}})
			require.NoError(t, err)
			quoted := pgx.Identifier{namespace}.Sanitize()
			_, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+quoted)
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, cleanupErr := admin.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE")
				require.NoError(t, cleanupErr)
				require.NoError(t, admin.Close())
			})
			parsed, err := url.Parse(address)
			require.NoError(t, err)
			query := parsed.Query()
			query.Set("search_path", namespace)
			parsed.RawQuery = query.Encode()
			t.Setenv("STARPORT_TEST_AUTH_SQL", parsed.String())
			t.Setenv("STARPORT_TEST_AUTH_NAMESPACE", namespace+":")
			store, db := fleetStores(t)
			require.NoError(t, db.Migrate(t.Context()))
			accounts, err := account.Open(store)
			require.NoError(t, err)
			_, err = accounts.EnsureDefault(t.Context())
			require.NoError(t, err)
			identities, err := identity.Open(db)
			require.NoError(t, err)
			team, err := identities.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "Team"})
			require.NoError(t, err)
			keys, err := apikey.Open(store)
			require.NoError(t, err)
			key, err := keys.Create(t.Context(), apikey.APIKey{ID: "key", Name: "fleet-key", Hash: "fleet-fixture-hash", Scopes: []string{"chat:write"}, AccountID: account.DefaultID, TeamID: team.Team.ID, Active: true})
			require.NoError(t, err)

			peer := startAuthorizationReplica(t)
			require.Equal(t, "permitted", peer.exchange(t, "check"))
			if owner == "kv" {
				key.APIKey.Active = false
				_, err = keys.Update(t.Context(), key.APIKey, key.Revision)
			} else {
				err = identities.Teams.Delete(t.Context(), team.Team.ID, team.Revision)
			}
			require.NoError(t, err)
			// The write is durable while this replica has not started its revision monitor.
			require.Equal(t, "permitted", peer.exchange(t, "check"))
			started := time.Now()
			require.Equal(t, "monitoring", peer.exchange(t, "monitor"))
			for peer.exchange(t, "check") != "withdrawn" {
				require.Less(t, time.Since(started), 2*time.Second, "replica did not enforce the withdrawal")
				time.Sleep(10 * time.Millisecond)
			}
			require.Less(t, time.Since(started), 2*time.Second)
			t.Logf("%s withdrawal observed by separate process after %s", owner, time.Since(started))
			require.Equal(t, "withdrawn", peer.exchange(t, "check"))
			require.Equal(t, "stopped", peer.exchange(t, "stop"))
			require.NoError(t, peer.command.Wait(), peer.stderr.String())
			peer.finished = true
		})
	}
}

func fleetStores(t *testing.T) (storage.KVStore, *sqlstore.DB) {
	t.Helper()
	raw, err := storage.OpenValkey(storage.ValkeyConfig{URL: os.Getenv("TEST_VALKEY_URL")})
	require.NoError(t, err)
	prefix := os.Getenv("STARPORT_TEST_AUTH_NAMESPACE")
	require.NotEmpty(t, prefix)
	t.Cleanup(func() {
		if os.Getenv("STARPORT_TEST_AUTH_REPLICA") != "1" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			keys, scanErr := raw.ScanWithPrefix(ctx, prefix, 0)
			require.NoError(t, scanErr)
			if len(keys) != 0 {
				require.NoError(t, raw.BatchDelete(ctx, keys))
			}
		}
		require.NoError(t, raw.Close())
	})
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: os.Getenv("STARPORT_TEST_AUTH_SQL")}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return repotest.NamespacedStore(raw, prefix), db
}

type authorizationReplica struct {
	command  *exec.Cmd
	input    io.WriteCloser
	output   <-chan string
	stderr   *bytes.Buffer
	finished bool
}

func startAuthorizationReplica(t *testing.T) *authorizationReplica {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, executable, "-test.run=^TestFleetDurableReceiptPrecedesReplicaEnforcement$", "-test.timeout=25s")
	command.Env = append(os.Environ(), "STARPORT_TEST_AUTH_REPLICA=1")
	input, err := command.StdinPipe()
	require.NoError(t, err)
	output, err := command.StdoutPipe()
	require.NoError(t, err)
	peer := &authorizationReplica{command: command, input: input, stderr: new(bytes.Buffer)}
	command.Stderr = peer.stderr
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		_ = input.Close()
		if !peer.finished {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	lines := make(chan string, 16)
	peer.output = lines
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if line, ok := strings.CutPrefix(scanner.Text(), "CSP-AUTH "); ok {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	require.Equal(t, "ready", peer.read(t))
	return peer
}

func (p *authorizationReplica) read(t *testing.T) string {
	t.Helper()
	select {
	case value, ok := <-p.output:
		require.True(t, ok, "replica closed its response stream")
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("replica response deadline exceeded")
		return ""
	}
}

func (p *authorizationReplica) exchange(t *testing.T, command string) string {
	t.Helper()
	_, err := fmt.Fprintln(p.input, command)
	require.NoError(t, err)
	return p.read(t)
}

func runAuthorizationReplica(t *testing.T) {
	store, db := fleetStores(t)
	kv, sql := revision.NewKV(store, nil), revision.NewSQL(db, nil)
	ks, err := kv.Initialize(t.Context())
	require.NoError(t, err)
	ss, err := sql.Initialize(t.Context())
	require.NoError(t, err)
	set, err := NewAuthoritySet(NewFence("kv", ks.Epoch), NewFence("sql", ss.Epoch))
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	accounts, err := account.Open(store)
	require.NoError(t, err)
	identities, err := identity.Open(db)
	require.NoError(t, err)
	clock := func() (time.Time, bool) { return time.Now(), true }
	source, err := NewRepositorySource(RepositorySources{Keys: keys, Accounts: accounts, Teams: identities.Teams, KV: kv, SQL: sql, KVAuthority: "kv", SQLAuthority: "sql"}, set, clock, time.Minute)
	require.NoError(t, err)
	limits := cacheTestLimits()
	limits.PermissionLifetime, limits.ClockUncertainty = time.Minute, 0
	cache, err := NewCache(source, set, limits, clock, hostclock.Elapsed)
	require.NoError(t, err)
	defer cache.Close()
	bundle, err := cache.Resolve(t.Context(), Identity{Subject: "fleet-fixture-hash"})
	require.NoError(t, err)
	monitor, err := NewMonitor([]WatchedAuthority{{Authority: "kv", Reader: kv}, {Authority: "sql", Reader: sql}}, set, time.Second, time.Second)
	require.NoError(t, err)
	defer func() { require.NoError(t, monitor.Close(context.Background())) }()
	_, err = fmt.Fprintln(os.Stdout, "CSP-AUTH ready")
	require.NoError(t, err)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		response := ""
		switch scanner.Text() {
		case "check":
			err := bundle.Permit().Check(time.Now(), true)
			switch {
			case err == nil:
				response = "permitted"
			case errors.Is(err, ErrWithdrawn):
				response = "withdrawn"
			default:
				t.Fatalf("unexpected permission state: %v", err)
			}
		case "monitor":
			monitor.Start(t.Context())
			response = "monitoring"
		case "stop":
			response = "stopped"
		default:
			t.Fatal("unknown replica command")
		}
		_, err := fmt.Fprintln(os.Stdout, "CSP-AUTH "+response)
		require.NoError(t, err)
		if response == "stopped" {
			return
		}
	}
	require.NoError(t, scanner.Err())
}
