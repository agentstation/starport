-- Baseline: the store's own metadata. Owning one durable table from the
-- first open proves the migration path on every deployment before any
-- concept schema arrives, and gives the store a place for facts about
-- itself (creation marker, future maintenance state).
CREATE TABLE IF NOT EXISTS sqlstore_meta (
    name TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO sqlstore_meta (name, value) VALUES ('schema', 'starport')
ON CONFLICT (name) DO NOTHING;
