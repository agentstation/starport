-- An incident transition is one change this gateway observed in a
-- provider's own status-page verdict: the indicator it moved to and when
-- this deployment's clock saw it. The live projection stays in memory on
-- the routing path; this table is the durable record the console reads,
-- so "what did the status page say at 3am" survives a restart. Timestamps
-- are RFC 3339 UTC strings, so text order is time order.
--
-- MySQL auto-commits DDL, so this file stays idempotent-safe: IF NOT
-- EXISTS lets a retried migration pass. Indexed columns are bounded
-- because MySQL cannot index an unbounded TEXT column, and the index is
-- declared inline because MySQL has no CREATE INDEX IF NOT EXISTS.
CREATE TABLE IF NOT EXISTS incident_transitions (
    provider_id VARCHAR(191) NOT NULL,
    indicator VARCHAR(32) NOT NULL,
    description TEXT NOT NULL,
    observed_at VARCHAR(64) NOT NULL,
    INDEX incident_transitions_provider_time (provider_id, observed_at)
);
