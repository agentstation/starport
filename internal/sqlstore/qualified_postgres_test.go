package sqlstore

import (
	"os"
	"strings"
	"testing"
)

// TestQualifiedPostgreSQLVersion proves that the real test service is the qualified PostgreSQL release.
// A different release fails here until a qualification run records it in QualifiedPostgreSQLVersion.
func TestQualifiedPostgreSQLVersion(t *testing.T) {
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("UNVERIFIED: TEST_POSTGRES_URL is not set")
	}
	db, err := Open(isolatedContractConfig(t, Config{Type: TypePostgres, Postgres: PostgresConfig{URL: url}}))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version string
	if err := db.QueryRowContext(t.Context(), "SHOW server_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	release, _, _ := strings.Cut(version, " ")
	if release != QualifiedPostgreSQLVersion {
		t.Fatalf("the service runs PostgreSQL %q, the qualified release is %q", version, QualifiedPostgreSQLVersion)
	}
}
