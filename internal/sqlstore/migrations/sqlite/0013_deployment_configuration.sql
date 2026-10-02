-- The head selects the approved shared configuration revision of one deployment.
-- One namespace belongs to one deployment.
CREATE TABLE IF NOT EXISTS deployment_configuration_head (
    deployment_id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL UNIQUE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    revision_id TEXT NOT NULL
);
-- Revisions are append-only. The record holds deployment-scope catalog values.
-- Credentials in the record are sealed with the master key.
CREATE TABLE IF NOT EXISTS deployment_configuration_revisions (
    revision_id TEXT PRIMARY KEY,
    deployment_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    predecessor TEXT NOT NULL,
    operation_id TEXT NOT NULL UNIQUE,
    actor TEXT NOT NULL,
    checksum TEXT NOT NULL,
    record TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (deployment_id, sequence)
);
