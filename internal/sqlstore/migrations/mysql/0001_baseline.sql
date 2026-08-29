-- Baseline: the store's own metadata. Owning one durable table from the
-- first open proves the migration path on every deployment before any
-- concept schema arrives, and gives the store a place for facts about
-- itself (creation marker, future maintenance state).
-- MySQL margins: a primary key needs a bounded column, and upsert is
-- INSERT IGNORE rather than ON CONFLICT.
CREATE TABLE IF NOT EXISTS sqlstore_meta (
    name VARCHAR(191) PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT IGNORE INTO sqlstore_meta (name, value) VALUES ('schema', 'starport');
