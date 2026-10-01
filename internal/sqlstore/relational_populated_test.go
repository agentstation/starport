package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var populatedScenarios = []string{"fresh", "activated"}

func populatedDialects(t *testing.T) []string {
	t.Helper()
	var dialects []string
	if os.Getenv("TEST_POSTGRES_URL") != "" {
		dialects = append(dialects, TypePostgres)
	}
	if os.Getenv("TEST_MYSQL_DSN") != "" {
		dialects = append(dialects, TypeMySQL)
	}
	if len(dialects) == 0 {
		t.Skip("TEST_POSTGRES_URL or TEST_MYSQL_DSN is required")
	}
	return dialects
}

func populatedConfig(t *testing.T, dialect string) Config {
	t.Helper()
	if dialect == TypePostgres {
		return isolatedContractConfig(t, Config{Type: TypePostgres, Postgres: PostgresConfig{URL: os.Getenv("TEST_POSTGRES_URL")}})
	}
	return isolatedContractConfig(t, Config{Type: TypeMySQL, MySQL: MySQLConfig{DSN: os.Getenv("TEST_MYSQL_DSN")}})
}

// populatedLive builds a populated deployment. The activated scenario retains historical receipts and both current rows.
func populatedLive(t *testing.T, dialect, scenario string) (*DB, Config) {
	t.Helper()
	config := populatedConfig(t, dialect)
	db := transferDB(t, config)
	if scenario == "fresh" {
		seedRelationalTransfer(t, db)
		return db, config
	}
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	image, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	identity := RelationalImportIdentity{OperationID: "earlier-import", RestrictionID: "closed"}
	require.NoError(t, db.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), image.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
	require.NoError(t, db.ReconcileRelationalImport(t.Context(), image.Snapshot, identity, "earlier-repair", strings.Repeat("e", 64), func(context.Context, *sql.Conn) error { return nil }))
	step := RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: strings.Repeat("b", 64)}
	receipt, err := db.ReplayRelationalImport(t.Context(), image.Snapshot, identity, step, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=epoch+1")
		return err
	})
	require.NoError(t, err)
	require.NoError(t, db.ActivateRelationalImportAt(t.Context(), image.Snapshot, identity, RelationalReplayPosition{Sequence: 1, ReceiptSHA256: receipt}, strings.Repeat("d", 64), func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=1")
		return err
	}))
	_, err = db.ExecContext(t.Context(), "INSERT INTO teams VALUES('later-team',1,'{}')")
	require.NoError(t, err)
	controls := populatedControls(t, db)
	require.Contains(t, controls, relationalActivationCurrent)
	require.Contains(t, controls, relationalReplayCurrent)
	return db, config
}

// capturePopulated records image C and its census through the same path as a deployment capture.
func capturePopulated(t *testing.T, db *DB) (SQLiteSnapshot, RelationalRecoveryCensus) {
	t.Helper()
	snapshot, census, _ := capturePopulatedImage(t, db)
	return snapshot, census
}

func capturePopulatedImage(t *testing.T, db *DB) (SQLiteSnapshot, RelationalRecoveryCensus, string) {
	t.Helper()
	parent := transferDirectory(t)
	result, err := db.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	path := filepath.Join(parent, "snapshot", "starport.db")
	view, err := OpenRelationalSnapshot(t.Context(), path, result.Snapshot, parent)
	require.NoError(t, err)
	census, err := view.RecoveryCensus(t.Context())
	require.NoError(t, err)
	require.NoError(t, view.Close())
	return result.Snapshot, census, path
}

func populatedCensus(t *testing.T, db *DB) RelationalRecoveryCensus {
	t.Helper()
	_, census := capturePopulated(t, db)
	return census
}

func populatedControls(t *testing.T, db *DB) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT name,value FROM sqlstore_meta")
	require.NoError(t, err)
	defer rows.Close()
	controls := map[string]string{}
	for rows.Next() {
		var name, value string
		require.NoError(t, rows.Scan(&name, &value))
		if recoveryControlName(name) {
			controls[name] = value
		}
	}
	require.NoError(t, rows.Err())
	return controls
}

func populatedDeadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func requireSamePopulatedDomain(t *testing.T, expected, actual RelationalRecoveryCensus) {
	t.Helper()
	require.Equal(t, expected.DomainSHA256, actual.DomainSHA256)
	require.Equal(t, expected.DomainRows, actual.DomainRows)
	require.Equal(t, expected.AuditHighWater, actual.AuditHighWater)
	require.Equal(t, expected.RevisionSHA256, actual.RevisionSHA256)
	require.Equal(t, expected.RevisionRows, actual.RevisionRows)
}

func TestRelationalPopulatedClaimThroughActivation(t *testing.T) {
	for _, dialect := range populatedDialects(t) {
		for _, scenario := range populatedScenarios {
			t.Run(dialect+"/"+scenario, func(t *testing.T) {
				live, config := populatedLive(t, dialect, scenario)
				snapshot, census, path := capturePopulatedImage(t, live)
				require.ErrorIs(t, live.ImportRelationalOnce(t.Context(), path, snapshot, transferDirectory(t), RelationalImportIdentity{OperationID: "empty", RestrictionID: "closed"}, restrictImportedFixture), ErrNotFresh)
				before := populatedControls(t, live)
				identity := RelationalImportIdentity{OperationID: "populated-adoption", RestrictionID: "closed-adoption"}
				var calls atomic.Int32
				restrict := func(ctx context.Context, conn *sql.Conn) error {
					calls.Add(1)
					return restrictImportedFixture(ctx, conn)
				}
				require.NoError(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrict))
				require.EqualValues(t, 1, calls.Load())

				claim, err := relationalImportClaim(snapshot, identity)
				require.NoError(t, err)
				claimDigest := sha256.Sum256(claim)
				encodedCensus, err := json.Marshal(census)
				require.NoError(t, err)
				censusDigest := sha256.Sum256(encodedCensus)
				key := relationalPopulatedPrefix + hex.EncodeToString(claimDigest[:])
				expected := maps.Clone(before)
				delete(expected, relationalActivationCurrent)
				delete(expected, relationalReplayCurrent)
				expected[relationalImportMarker] = string(claim)
				expected[key] = `{"version":1,"claim_sha256":"` + hex.EncodeToString(claimDigest[:]) + `","census_sha256":"` + hex.EncodeToString(censusDigest[:]) + `"}`
				require.Equal(t, expected, populatedControls(t, live), "the claim changes only the marker, the closure receipt, and the current rows")
				claimed := populatedCensus(t, live)
				requireSamePopulatedDomain(t, census, claimed)

				// A lost reply retries from the same handle and from a new process handle.
				require.NoError(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrict))
				second, err := Open(config)
				require.NoError(t, err)
				defer second.Close()
				require.NoError(t, second.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrict))
				require.EqualValues(t, 1, calls.Load(), "exact retries must not repeat restrict")
				require.Equal(t, claimed, populatedCensus(t, live))
				require.ErrorIs(t, live.ImportRelationalOnce(t.Context(), path, snapshot, transferDirectory(t), RelationalImportIdentity{OperationID: "empty", RestrictionID: "closed"}, restrictImportedFixture), ErrImportRestricted)
				_, err = InspectRecoveryStartup(t.Context(), config, "deployment")
				require.ErrorIs(t, err, ErrImportRestricted)

				// The shared import phases treat the populated claim as an opaque claim.
				position := RelationalReplayPosition{}
				guarded := 0
				require.NoError(t, live.GuardRelationalImport(populatedDeadline(t), snapshot, identity, position, func(context.Context, *sql.Conn) error { guarded++; return nil }))
				require.Equal(t, 1, guarded)
				require.NoError(t, live.CheckRelationalImportPosition(t.Context(), snapshot, identity, position))
				require.NoError(t, live.CheckUnreleasedRelationalImport(t.Context(), snapshot, identity))
				image, err := live.SnapshotRelationalImport(t.Context(), filepath.Join(transferDirectory(t), "snapshot"), snapshot, identity, position)
				require.NoError(t, err)
				require.True(t, image.Published)
				noop := func(context.Context, *sql.Conn) error { return nil }
				require.NoError(t, live.ReconcileRelationalImport(t.Context(), snapshot, identity, "populated-repair", strings.Repeat("c", 64), noop))
				step := RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: strings.Repeat("f", 64)}
				receipt, err := live.ReplayRelationalImport(t.Context(), snapshot, identity, step, noop)
				require.NoError(t, err)
				position = RelationalReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
				decision := strings.Repeat("9", 64)
				require.NoError(t, live.ActivateRelationalImportAt(t.Context(), snapshot, identity, position, decision, func(ctx context.Context, conn *sql.Conn) error {
					_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=1")
					return err
				}))
				require.NoError(t, live.CheckActivatedRelationalImportAt(t.Context(), snapshot, identity, position, decision))
				require.NoError(t, live.CheckImportBarrier(t.Context()))
				state, err := InspectRecoveryStartup(t.Context(), config, "deployment")
				require.NoError(t, err)
				require.True(t, state.Activated)
				require.Equal(t, decision, state.DecisionSHA256)

				activated := populatedCensus(t, live)
				requireSamePopulatedDomain(t, census, activated)
				after := populatedControls(t, live)
				for name, value := range before {
					if name != relationalActivationCurrent && name != relationalReplayCurrent {
						require.Equal(t, value, after[name], "historical receipt %s", name)
					}
				}
				require.Equal(t, expected[key], after[key], "activation retains the closure receipt")
				require.ErrorIs(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrict), ErrPopulatedCensus)
				require.EqualValues(t, 1, calls.Load())
				require.Equal(t, activated, populatedCensus(t, live))
			})
		}
	}
}

func TestRelationalPopulatedClaimRefusesChangedRows(t *testing.T) {
	exec := func(queries ...string) func(*testing.T, *DB, RelationalRecoveryCensus) {
		return func(t *testing.T, db *DB, _ RelationalRecoveryCensus) {
			for _, query := range queries {
				_, err := db.ExecContext(t.Context(), db.Bind(query))
				require.NoError(t, err)
			}
		}
	}
	mutations := map[string]func(*testing.T, *DB, RelationalRecoveryCensus){
		"changed-domain":   exec(`UPDATE users SET record='{"name":"changed"}' WHERE id='user'`),
		"extra-row":        exec("INSERT INTO teams VALUES('extra-team',1,'{}')"),
		"missing-row":      exec("DELETE FROM incident_transitions WHERE provider_id='provider'"),
		"changed-witness":  exec("UPDATE catalog_recovery SET evidence='changed-proof'"),
		"changed-revision": exec("UPDATE authorization_revision SET sequence=sequence-1"),
		"changed-activation-current": exec("DELETE FROM sqlstore_meta WHERE name='"+relationalActivationCurrent+"'",
			"INSERT INTO sqlstore_meta(name,value) VALUES('"+relationalActivationCurrent+"','changed')"),
		"changed-replay-current": exec("DELETE FROM sqlstore_meta WHERE name='"+relationalReplayCurrent+"'",
			"INSERT INTO sqlstore_meta(name,value) VALUES('"+relationalReplayCurrent+"','changed')"),
		"changed-historical-receipt": exec("INSERT INTO sqlstore_meta(name,value) VALUES('relational-reconciliation-v1:extra','{}')"),
		"advanced-audit-counter": func(t *testing.T, db *DB, census RelationalRecoveryCensus) {
			require.NoError(t, restoreAuditHighWater(t.Context(), db, db, db.dialect, census.AuditHighWater+10))
		},
	}
	for _, dialect := range populatedDialects(t) {
		for _, scenario := range populatedScenarios {
			for _, name := range slices.Sorted(maps.Keys(mutations)) {
				t.Run(dialect+"/"+scenario+"/"+name, func(t *testing.T) {
					live, _ := populatedLive(t, dialect, scenario)
					snapshot, census := capturePopulated(t, live)
					mutations[name](t, live, census)
					before := populatedCensus(t, live)
					require.NotEqual(t, census, before)
					calls := 0
					restrict := func(context.Context, *sql.Conn) error { calls++; return nil }
					identity := RelationalImportIdentity{OperationID: "populated-adoption", RestrictionID: "closed"}
					require.ErrorIs(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrict), ErrPopulatedCensus)
					require.Zero(t, calls)
					require.Equal(t, before, populatedCensus(t, live), "a refusal leaves no change")
					require.NoError(t, live.CheckImportBarrier(t.Context()))
				})
			}
		}
	}
}

func TestRelationalPopulatedClaimConflictsLeaveNoChange(t *testing.T) {
	for _, dialect := range populatedDialects(t) {
		t.Run(dialect, func(t *testing.T) {
			noop := func(context.Context, *sql.Conn) error { return nil }
			identity := RelationalImportIdentity{OperationID: "populated-adoption", RestrictionID: "closed"}
			t.Run("restrict-error", func(t *testing.T) {
				live, _ := populatedLive(t, dialect, "activated")
				snapshot, census := capturePopulated(t, live)
				interrupted := errors.New("witness transition interrupted")
				err := live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, func(ctx context.Context, conn *sql.Conn) error {
					require.NoError(t, restrictImportedFixture(ctx, conn))
					return interrupted
				})
				require.ErrorIs(t, err, interrupted)
				require.Equal(t, census, populatedCensus(t, live))
				require.NoError(t, live.CheckImportBarrier(t.Context()))
				require.NoError(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrictImportedFixture))
			})
			t.Run("claimed", func(t *testing.T) {
				live, config := populatedLive(t, dialect, "fresh")
				snapshot, census := capturePopulated(t, live)
				require.NoError(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrictImportedFixture))
				claimed := populatedCensus(t, live)
				other := census
				other.WitnessSHA256 = strings.Repeat("0", 64)
				refusals := map[string]func() error{
					"different-identity": func() error {
						return live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), RelationalImportIdentity{OperationID: "other-adoption", RestrictionID: "closed"}, noop)
					},
					"different-snapshot": func() error {
						changed := snapshot
						changed.SHA256 = strings.Repeat("0", 64)
						return live.ClaimPopulatedRelational(t.Context(), changed, census, transferDirectory(t), identity, noop)
					},
					"different-census": func() error {
						return live.ClaimPopulatedRelational(t.Context(), snapshot, other, transferDirectory(t), identity, noop)
					},
				}
				for _, name := range slices.Sorted(maps.Keys(refusals)) {
					require.ErrorIs(t, refusals[name](), ErrImportRestricted, name)
					require.Equal(t, claimed, populatedCensus(t, live), name)
				}
				claim, err := relationalImportClaim(snapshot, identity)
				require.NoError(t, err)
				digest := sha256.Sum256(claim)
				key := relationalPopulatedPrefix + hex.EncodeToString(digest[:])
				var receipt string
				require.NoError(t, live.QueryRowContext(t.Context(), live.Bind("SELECT value FROM sqlstore_meta WHERE name=?"), key).Scan(&receipt))
				for _, change := range []string{"UPDATE sqlstore_meta SET value='{}' WHERE name=?", "DELETE FROM sqlstore_meta WHERE name=?"} {
					_, err := live.ExecContext(t.Context(), live.Bind(change), key)
					require.NoError(t, err)
					changed := populatedCensus(t, live)
					require.ErrorIs(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, noop), ErrImportRestricted, change)
					require.Equal(t, changed, populatedCensus(t, live), change)
					_, err = live.ExecContext(t.Context(), live.Bind("DELETE FROM sqlstore_meta WHERE name=?"), key)
					require.NoError(t, err)
					_, err = live.ExecContext(t.Context(), live.Bind("INSERT INTO sqlstore_meta(name,value) VALUES(?,?)"), key, receipt)
					require.NoError(t, err)
				}
				require.NoError(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, noop))
				require.Equal(t, claimed, populatedCensus(t, live))
				_, err = live.ExecContext(t.Context(), "DELETE FROM sqlstore_meta WHERE name='"+relationalImportMarker+"'")
				require.NoError(t, err)
				_, err = InspectRecoveryStartup(t.Context(), config, "deployment")
				require.ErrorIs(t, err, ErrImportRestricted, "a closure receipt without activation must not become first boot")
			})
			t.Run("invalid-input", func(t *testing.T) {
				live, _ := populatedLive(t, dialect, "fresh")
				snapshot, census := capturePopulated(t, live)
				require.ErrorContains(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, nil), "restriction callback")
				require.ErrorIs(t, live.ClaimPopulatedRelational(t.Context(), snapshot, RelationalRecoveryCensus{}, transferDirectory(t), identity, noop), ErrImportRestricted)
				require.Error(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), RelationalImportIdentity{}, noop))
				require.Error(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, filepath.Join(t.TempDir(), "absent"), identity, noop))
				require.Equal(t, census, populatedCensus(t, live))
				require.NoError(t, live.CheckImportBarrier(t.Context()))
			})
		})
	}
}

func TestRelationalPopulatedClaimLocksWritersAndHasOneRestriction(t *testing.T) {
	for _, dialect := range populatedDialects(t) {
		t.Run(dialect, func(t *testing.T) {
			live, config := populatedLive(t, dialect, "activated")
			snapshot, census := capturePopulated(t, live)
			identity := RelationalImportIdentity{OperationID: "populated-adoption", RestrictionID: "closed"}
			writer, err := Open(config)
			require.NoError(t, err)
			defer writer.Close()
			second, err := Open(config)
			require.NoError(t, err)
			defer second.Close()
			var calls atomic.Int32
			written := make(chan error, 1)
			restrict := func(ctx context.Context, conn *sql.Conn) error {
				calls.Add(1)
				go func() {
					_, err := writer.ExecContext(t.Context(), `UPDATE users SET record='{"name":"late writer"}' WHERE id='user'`)
					written <- err
				}()
				select {
				case err := <-written:
					return errors.Join(errors.New("a writer changed a locked table during the claim"), err)
				case <-time.After(300 * time.Millisecond):
				}
				return restrictImportedFixture(ctx, conn)
			}
			var group sync.WaitGroup
			results := make(chan error, 2)
			for _, db := range []*DB{live, second} {
				group.Go(func() {
					results <- db.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrict)
				})
			}
			group.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			require.EqualValues(t, 1, calls.Load(), "concurrent exact claims run restrict once")
			require.NoError(t, <-written, "the blocked writer proceeds after commit")
			require.NoError(t, live.CheckRelationalImportPosition(t.Context(), snapshot, identity, RelationalReplayPosition{}))
		})
	}
}

func TestRelationalPopulatedClaimRefusesSQLite(t *testing.T) {
	db, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, db)
	snapshot, census := capturePopulated(t, db)
	calls := 0
	identity := RelationalImportIdentity{OperationID: "populated-adoption", RestrictionID: "closed"}
	require.ErrorIs(t, db.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, func(context.Context, *sql.Conn) error { calls++; return nil }), ErrPopulatedBackend)
	require.Zero(t, calls)
	require.Equal(t, census, populatedCensus(t, db))
	require.NoError(t, db.CheckImportBarrier(t.Context()))
	var absent *DB
	require.ErrorIs(t, absent.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrictImportedFixture), ErrClosed)
}

func TestRelationalPopulatedReceiptIsNativeControl(t *testing.T) {
	db, _ := sqliteSnapshotFixture(t)
	original := recoveryCensusFixture(t, db)
	_, err := db.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES(?,'{}')", relationalPopulatedPrefix+strings.Repeat("a", 64))
	require.NoError(t, err)
	control := recoveryCensusFixture(t, db)
	require.Equal(t, original.DomainSHA256, control.DomainSHA256)
	require.Equal(t, original.DomainRows, control.DomainRows)
	require.Equal(t, original.ControlRows+1, control.ControlRows)
	require.NotEqual(t, original.ControlSHA256, control.ControlSHA256)
	path := filepath.Join(t.TempDir(), "private", "live.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	orphan := transferDB(t, Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: path}})
	_, err = orphan.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES(?,'{}')", relationalPopulatedPrefix+strings.Repeat("a", 64))
	require.NoError(t, err)
	_, err = InspectRecoveryStartup(t.Context(), Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: path}}, "deployment")
	require.ErrorIs(t, err, ErrImportRestricted, "a closure receipt without activation must not become first boot")
}

func TestRelationalPopulatedCensusIgnoresServerCollation(t *testing.T) {
	for _, dialect := range populatedDialects(t) {
		t.Run(dialect, func(t *testing.T) {
			live, _ := populatedLive(t, dialect, "fresh")
			// Keys stay distinct under case-insensitive and accent-insensitive collations but sort differently in binary order.
			values := []string{
				`INSERT INTO users VALUES('alpha','provider:alpha',1,'{"name":"Ωmega 😀"}')`,
				`INSERT INTO users VALUES('Bravo','provider:Bravo',2,'null')`,
				`INSERT INTO users VALUES('Éclair','provider:Éclair',3,'NULL')`,
				`INSERT INTO users VALUES('ecru','provider:ecru',4,'')`,
				`INSERT INTO users VALUES('世界','provider:世界',5,'e` + "́" + ` trailing ')`,
				"INSERT INTO users VALUES('zulu','provider:zulu',6,'\x01\x02\t\n\r\x1f\x7f')",
				`INSERT INTO users VALUES('','provider:',7,'{}')`,
				`INSERT INTO teams VALUES('Team-B',1,'')`,
				`INSERT INTO teams VALUES('team-c',1,'ß')`,
				`INSERT INTO sqlstore_meta(name,value) VALUES('Mixed-Case-Key','')`,
				`INSERT INTO sqlstore_meta(name,value) VALUES('mixed-case-other','NULL')`,
				`INSERT INTO account_grants VALUES('Account','','','')`,
				`INSERT INTO incident_transitions VALUES('Provider','','` + "\x7f" + `','2026-09-30T00:00:00Z')`,
			}
			for _, query := range values {
				_, err := live.ExecContext(t.Context(), query)
				require.NoError(t, err, query)
			}
			snapshot, census := capturePopulated(t, live)
			serverOrder := []string{}
			rows, err := live.QueryContext(t.Context(), "SELECT id FROM users ORDER BY id")
			require.NoError(t, err)
			for rows.Next() {
				var id string
				require.NoError(t, rows.Scan(&id))
				serverOrder = append(serverOrder, id)
			}
			require.NoError(t, errors.Join(rows.Err(), rows.Close()))
			binaryOrder := slices.Sorted(slices.Values(serverOrder))
			t.Logf("server order %q, binary order %q", serverOrder, binaryOrder)
			if dialect == TypeMySQL {
				require.NotEqual(t, binaryOrder, serverOrder, "the fixture must exercise a collation order that differs from binary order")
			}
			owner, err := live.acquireMigrationOwner(t.Context())
			require.NoError(t, err)
			cleanup, err := prepareMySQLTransfer(t.Context(), owner)
			require.NoError(t, err)
			var direct RelationalRecoveryCensus
			require.NoError(t, live.lockedRelationalTransaction(t.Context(), owner, func() error {
				direct, err = live.populatedRecoveryCensus(t.Context(), owner.conn, transferDirectory(t))
				return err
			}))
			require.NoError(t, errors.Join(cleanup(), owner.close()))
			require.Equal(t, census, direct)
			identity := RelationalImportIdentity{OperationID: "populated-adoption", RestrictionID: "closed"}
			require.NoError(t, live.ClaimPopulatedRelational(t.Context(), snapshot, census, transferDirectory(t), identity, restrictImportedFixture))
		})
	}
}
