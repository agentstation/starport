-- Keep migration repair evidence portable across relational backends.
CREATE TABLE IF NOT EXISTS schema_migration_reconciliations (
    operation_id VARCHAR(191) PRIMARY KEY,
    name VARCHAR(191) NOT NULL,
    digest VARCHAR(64) NOT NULL,
    outcome VARCHAR(16) NOT NULL,
    actor VARCHAR(191) NOT NULL,
    evidence_sha256 VARCHAR(64) NOT NULL,
    recorded_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
