-- The head selects the approved shared configuration revision of one deployment.
-- One namespace belongs to one deployment.
CREATE TABLE IF NOT EXISTS deployment_configuration_head (
    deployment_id VARCHAR(256) PRIMARY KEY,
    namespace VARCHAR(512) NOT NULL UNIQUE,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    revision_id VARCHAR(64) NOT NULL
);
-- Revisions are append-only. The record holds deployment-scope catalog values.
-- Credentials in the record are sealed with the master key.
CREATE TABLE IF NOT EXISTS deployment_configuration_revisions (
    revision_id VARCHAR(64) PRIMARY KEY,
    deployment_id VARCHAR(256) NOT NULL,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    predecessor VARCHAR(64) NOT NULL,
    operation_id VARCHAR(191) NOT NULL UNIQUE,
    actor VARCHAR(191) NOT NULL,
    checksum VARCHAR(64) NOT NULL,
    record TEXT NOT NULL,
    created_at VARCHAR(64) NOT NULL,
    UNIQUE (deployment_id, sequence)
);
