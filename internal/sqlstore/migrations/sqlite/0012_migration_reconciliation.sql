-- Keep migration repair evidence portable across relational backends.
CREATE TABLE IF NOT EXISTS schema_migration_reconciliations (
    operation_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    digest TEXT NOT NULL,
    outcome VARCHAR(16) NOT NULL,
    actor TEXT NOT NULL,
    evidence_sha256 TEXT NOT NULL,
    recorded_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
