package identity

import (
	"github.com/agentstation/starport/internal/sqlstore"
	"strings"
)

// boundedIdentityQuery suppresses oversized text before the driver receives it.
// Callers supply constant queries. Identifiers never come from requests.
func boundedIdentityQuery(dialect, query string) string {
	size := "OCTET_LENGTH(record)"
	if dialect == sqlstore.TypeSQLite {
		size = "LENGTH(CAST(record AS BLOB))"
	}
	return strings.Replace(query, "SELECT record", "SELECT CASE WHEN "+size+" > ? THEN NULL ELSE record END", 1)
}
