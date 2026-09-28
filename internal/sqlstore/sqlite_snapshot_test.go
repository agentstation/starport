package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/agentstation/starmap/pkg/productfiles"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSQLiteRawFileCopyLosesCommittedWAL(t *testing.T) {
	source := filepath.Join(t.TempDir(), "live.db")
	db, err := Open(Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO sqlstore_meta VALUES ('wal-only','committed')"); err != nil {
		t.Fatal(err)
	}
	wal, err := os.Stat(source + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("WAL missing: %v", err)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	copied, err := Open(Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: copyPath}})
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	var count int
	if err := copied.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlstore_meta WHERE name='wal-only'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("fixture did not reproduce missing WAL data: %d", count)
	}
	t.Log("Raw file copy omits a committed WAL record")
}

func sqliteSnapshotFixture(t *testing.T) (*DB, string) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "private")
	if _, err := productfiles.CreateDirectory(parent); err != nil {
		t.Fatal(err)
	}
	db, err := Open(Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: filepath.Join(parent, "live.db")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db, parent
}

func openSnapshotFixture(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func TestSQLiteSnapshotPreservesWALAndRestoresExactBytes(t *testing.T) {
	db, parent := sqliteSnapshotFixture(t)
	for _, query := range []string{
		"PRAGMA wal_checkpoint(TRUNCATE)", "PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE snapshot_values (id INTEGER PRIMARY KEY, bytes BLOB, exact_integer INTEGER, real_value REAL, empty TEXT, absent TEXT)",
		"INSERT INTO snapshot_values VALUES(99,x'00ff01',9223372036854775807,1.25,'',NULL)",
		"INSERT INTO sqlstore_meta VALUES('wal-only','committed')",
		"INSERT INTO catalog_recovery VALUES('deployment',7,1,'old-primary','independent-evidence',0)",
	} {
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	live := filepath.Join(parent, "live.db")
	wal, err := os.Stat(live + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("WAL not populated: %v", err)
	}
	backup := filepath.Join(parent, "backup")
	result, err := db.SnapshotSQLite(t.Context(), backup)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Published || result.Snapshot.Size == 0 || len(result.Snapshot.SHA256) != 64 {
		t.Fatalf("invalid result: %+v", result)
	}
	entries, err := os.ReadDir(backup)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "starport.db", "snapshot.json", ".record-publications":
		default:
			t.Fatalf("snapshot depends on unexpected file %q", entry.Name())
		}
	}
	// Modify the live database after the snapshot. Restore must retain the snapshot boundary.
	if _, err := db.ExecContext(t.Context(), "UPDATE sqlstore_meta SET value='later' WHERE name='wal-only'"); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(parent, "restored")
	imported, err := RestoreSQLiteSnapshot(t.Context(), restored, filepath.Join(backup, "starport.db"), result.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !imported.Published || imported.Snapshot != result.Snapshot {
		t.Fatalf("import result: %+v", imported)
	}
	recovered := openSnapshotFixture(t, filepath.Join(restored, "starport.db"))
	var value string
	if err := recovered.QueryRowContext(t.Context(), "SELECT value FROM sqlstore_meta WHERE name='wal-only'").Scan(&value); err != nil || value != "committed" {
		t.Fatalf("snapshot lost committed WAL: %q %v", value, err)
	}
	var blob []byte
	var integer int64
	var real float64
	var empty string
	var absent sql.NullString
	if err := recovered.QueryRowContext(t.Context(), "SELECT bytes,exact_integer,real_value,empty,absent FROM snapshot_values WHERE id=99").Scan(&blob, &integer, &real, &empty, &absent); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(blob, []byte{0, 255, 1}) || integer != math.MaxInt64 || real != 1.25 || empty != "" || absent.Valid {
		t.Fatalf("SQL values changed: %x %d %g %q %+v", blob, integer, real, empty, absent)
	}
	var epoch, gate int
	if err := recovered.QueryRowContext(t.Context(), "SELECT epoch,gate_open FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&epoch, &gate); err != nil || epoch != 7 || gate != 1 {
		t.Fatalf("raw import modified history: %d %d %v", epoch, gate, err)
	}
	// The coordinator owns restricting and reconciling this imported history before use.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(restored, "starport.db"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("database mode %o", info.Mode().Perm())
		}
	}
}

func TestSQLiteSnapshotRefusesExistingDestination(t *testing.T) {
	db, parent := sqliteSnapshotFixture(t)
	destination := filepath.Join(parent, "existing")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(destination, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := db.SnapshotSQLite(t.Context(), destination); !errors.Is(err, os.ErrExist) || r.Published {
		t.Fatalf("existing destination: %+v %v", r, err)
	}
	backup, err := db.SnapshotSQLite(t.Context(), filepath.Join(parent, "backup"))
	if err != nil {
		t.Fatal(err)
	}
	if r, err := RestoreSQLiteSnapshot(t.Context(), destination, filepath.Join(parent, "backup", "starport.db"), backup.Snapshot); !errors.Is(err, os.ErrExist) || r.Published {
		t.Fatalf("existing restore: %+v %v", r, err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing data changed: %q %v", data, err)
	}
}

func TestSQLiteSnapshotRejectsCorruptOrUnknownImages(t *testing.T) {
	for _, scenario := range []string{"digest", "size", "format", "corrupt", "foreign-key", "future-history", "history-gap", "no-history"} {
		t.Run(scenario, func(t *testing.T) {
			db, parent := sqliteSnapshotFixture(t)
			result, err := db.SnapshotSQLite(t.Context(), filepath.Join(parent, "backup"))
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(parent, "backup", "starport.db")
			expected := result.Snapshot
			switch scenario {
			case "digest":
				expected.SHA256 = strings.Repeat("0", 64)
			case "size":
				expected.Size++
			case "format":
				expected.Format = "unknown"
			case "corrupt":
				if err := os.WriteFile(source, []byte("not a database"), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				modify, err := sql.Open("sqlite", source)
				if err != nil {
					t.Fatal(err)
				}
				query := ""
				switch scenario {
				case "foreign-key":
					query = "PRAGMA foreign_keys=OFF; CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER REFERENCES parent(id)); INSERT INTO child VALUES(42)"
				case "future-history":
					query = "INSERT INTO schema_migrations(name) VALUES('9999_future.sql')"
				case "history-gap":
					query = "DELETE FROM schema_migrations WHERE name='0002_account_templates.sql'"
				case "no-history":
					query = "DELETE FROM schema_migrations"
				}
				if _, err := modify.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
				if err := modify.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "digest" && scenario != "size" && scenario != "format" {
				data, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(data)
				expected.Size = int64(len(data))
				expected.SHA256 = hex.EncodeToString(sum[:])
			}
			destination := filepath.Join(parent, "restored")
			result, err = RestoreSQLiteSnapshot(t.Context(), destination, source, expected)
			if err == nil || result.Published {
				t.Fatalf("invalid image published: %+v %v", result, err)
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed restore published a destination: %v", err)
			}
			stages, err := filepath.Glob(filepath.Join(parent, ".sqlite-snapshot-*"))
			if err != nil || len(stages) != 0 {
				t.Fatalf("failed stages remain: %v %v", stages, err)
			}
		})
	}
}

func TestSQLiteSnapshotConcurrentPublicationHasOneWinner(t *testing.T) {
	db, parent := sqliteSnapshotFixture(t)
	destination := filepath.Join(parent, "winner")
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Go(func() { _, err := db.SnapshotSQLite(t.Context(), destination); results <- err })
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("publication winners %d", winners)
	}
}

func TestSQLiteSnapshotCancellationWhilePoolOccupied(t *testing.T) {
	db, parent := sqliteSnapshotFixture(t)
	connection, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	destination := filepath.Join(parent, "canceled")
	result, err := db.SnapshotSQLite(ctx, destination)
	if !errors.Is(err, context.DeadlineExceeded) || result.Published {
		t.Fatalf("canceled snapshot: %+v %v", result, err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled destination: %v", err)
	}
	stages, err := filepath.Glob(filepath.Join(parent, ".sqlite-snapshot-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("canceled stages: %v %v", stages, err)
	}
}

func TestSQLiteSnapshotKeepsConcurrentTransactionWhole(t *testing.T) {
	db, parent := sqliteSnapshotFixture(t)
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE snapshot_pair(id INTEGER PRIMARY KEY,value INTEGER); INSERT INTO snapshot_pair VALUES(1,0),(2,0)"); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: filepath.Join(parent, "live.db")}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	started := make(chan struct{})
	stop := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(started)
		for i := 1; ; i++ {
			select {
			case <-stop:
				finished <- nil
				return
			default:
			}
			tx, err := writer.BeginTx(t.Context(), nil)
			if err != nil {
				finished <- err
				return
			}
			_, firstErr := tx.ExecContext(t.Context(), "UPDATE snapshot_pair SET value=? WHERE id=1", i)
			_, secondErr := tx.ExecContext(t.Context(), "UPDATE snapshot_pair SET value=? WHERE id=2", i)
			if err := errors.Join(firstErr, secondErr); err != nil {
				_ = tx.Rollback()
				finished <- err
				return
			}
			if err := tx.Commit(); err != nil {
				finished <- err
				return
			}
		}
	}()
	<-started
	defer func() {
		close(stop)
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	for i := range 4 {
		destination := filepath.Join(parent, fmt.Sprintf("snapshot-%d", i))
		if _, err := db.SnapshotSQLite(t.Context(), destination); err != nil {
			t.Fatal(err)
		}
		snapshot := openSnapshotFixture(t, filepath.Join(destination, "starport.db"))
		var first, second int
		if err := snapshot.QueryRowContext(t.Context(), "SELECT a.value,b.value FROM snapshot_pair a JOIN snapshot_pair b ON a.id=1 AND b.id=2").Scan(&first, &second); err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Fatalf("partial transaction: %d != %d", first, second)
		}
	}
}

func TestSQLiteSnapshotAcceptsOlderKnownSchemaWithoutMigration(t *testing.T) {
	db, parent := sqliteSnapshotFixture(t)
	// Use a separate database with only the original metadata migration.
	old, err := Open(Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: filepath.Join(parent, "old.db")}})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := old.ExecContext(t.Context(), db.schemaMigrationsDDL()); err != nil {
		t.Fatal(err)
	}
	body, err := migrations.ReadFile("migrations/sqlite/0001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.ExecContext(t.Context(), string(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := old.ExecContext(t.Context(), "INSERT INTO schema_migrations(name) VALUES('0001_baseline.sql')"); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(parent, "older-snapshot")
	if _, err := old.SnapshotSQLite(t.Context(), destination); err != nil {
		t.Fatal(err)
	}
	copy := openSnapshotFixture(t, filepath.Join(destination, "starport.db"))
	var count int
	if err := copy.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot migrated older database: %d %v", count, err)
	}
}
