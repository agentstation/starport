CREATE TABLE IF NOT EXISTS catalog_recovery (
    deployment_id VARBINARY(128) PRIMARY KEY,
    epoch BIGINT NOT NULL CHECK (epoch > 0),
    gate_open INTEGER NOT NULL CHECK (gate_open IN (0, 1)),
    backend_id VARBINARY(256) NOT NULL,
    evidence BLOB NOT NULL
);
