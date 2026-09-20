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
	"sync"
	"sync/atomic"
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
	for _, scenario := range []struct {
		owner string
		live  bool
	}{{"kv", false}, {"sql", false}, {"kv", true}, {"sql", true}} {
		owner := scenario.owner
		t.Run(fmt.Sprintf("%s/live_%t", owner, scenario.live), func(t *testing.T) {
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

			peer := startAuthorizationReplica(t, "ready")
			require.Equal(t, "permitted", peer.exchange(t, "check"))
			if scenario.live {
				require.Equal(t, "loaded", peer.exchange(t, "load"))
				ready := time.Now()
				for peer.exchange(t, "observed") != "observed" {
					require.Less(t, time.Since(ready), 2*time.Second, "monitor did not observe both authorities")
					time.Sleep(10 * time.Millisecond)
				}
			}
			if owner == "kv" {
				key.APIKey.Active = false
				_, err = keys.Update(t.Context(), key.APIKey, key.Revision)
			} else {
				err = identities.Teams.Delete(t.Context(), team.Team.ID, team.Revision)
			}
			require.NoError(t, err)
			started := time.Now()
			if !scenario.live {
				// The write is durable before this replica starts its revision monitor.
				require.Equal(t, "permitted", peer.exchange(t, "check"))
				require.Equal(t, "monitoring", peer.exchange(t, "monitor"))
			}
			for peer.exchange(t, "check") != "withdrawn" {
				require.Less(t, time.Since(started), 2*time.Second, "replica did not enforce the withdrawal")
				time.Sleep(10 * time.Millisecond)
			}
			require.Less(t, time.Since(started), 2*time.Second)
			t.Logf("%s live=%t withdrawal observed by separate process after %s", owner, scenario.live, time.Since(started))
			if scenario.live {
				require.NotEqual(t, "checks=0", peer.exchange(t, "load-count"))
				t.Log(peer.exchange(t, "load-count"))
			}
			require.Equal(t, "withdrawn", peer.exchange(t, "check"))
			require.Equal(t, "stopped", peer.exchange(t, "stop"))
			require.NoError(t, peer.command.Wait(), peer.stderr.String())
			peer.finished = true
			t.Setenv("STARPORT_TEST_AUTH_REFUSAL", owner)
			restarted := startAuthorizationReplica(t, "refused")
			require.NoError(t, restarted.command.Wait(), restarted.stderr.String())
			restarted.finished = true
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

func startAuthorizationReplica(t *testing.T, expected string) *authorizationReplica {
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
	require.Equal(t, expected, peer.read(t))
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
	if owner := os.Getenv("STARPORT_TEST_AUTH_REFUSAL"); owner != "" {
		expected := ErrDenied
		if owner == "sql" {
			expected = identity.ErrTeamNotFound
		}
		require.ErrorIs(t, err, expected)
		require.Nil(t, bundle)
		_, writeErr := fmt.Fprintln(os.Stdout, "CSP-AUTH refused")
		require.NoError(t, writeErr)
		return
	}
	require.NoError(t, err)
	monitor, err := NewMonitor([]WatchedAuthority{{Authority: "kv", Reader: kv}, {Authority: "sql", Reader: sql}}, set, time.Second, time.Second)
	require.NoError(t, err)
	defer func() { require.NoError(t, monitor.Close(context.Background())) }()
	_, err = fmt.Fprintln(os.Stdout, "CSP-AUTH ready")
	require.NoError(t, err)
	loadContext, stopLoad := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	var checks atomic.Uint64
	defer func() { stopLoad(); workers.Wait() }()
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
		case "load":
			monitor.Start(t.Context())
			for range 16 {
				workers.Go(func() {
					ticker := time.NewTicker(time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-loadContext.Done():
							return
						case <-ticker.C:
							for range 32 {
								_ = bundle.Permit().Check(time.Now(), true)
								checks.Add(1)
							}
						}
					}
				})
			}
			response = "loaded"
		case "load-count":
			response = fmt.Sprintf("checks=%d", checks.Load())
		case "observed":
			response = "observed"
			for _, status := range monitor.Status() {
				if status.VerifiedAt.IsZero() || status.Failure != "" {
					response = "waiting"
				}
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
