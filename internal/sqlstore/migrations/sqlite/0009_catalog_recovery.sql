CREATE TABLE IF NOT EXISTS catalog_recovery (
    deployment_id TEXT PRIMARY KEY,
    epoch INTEGER NOT NULL CHECK (epoch > 0),
    gate_open INTEGER NOT NULL CHECK (gate_open IN (0, 1)),
    backend_id TEXT NOT NULL,
    evidence TEXT NOT NULL
);
