-- An incident transition is one change this gateway observed in a
-- provider's own status-page verdict: the indicator it moved to and when
-- this deployment's clock saw it. The live projection stays in memory on
-- the routing path; this table is the durable record the console reads,
-- so "what did the status page say at 3am" survives a restart. Timestamps
-- are RFC 3339 UTC strings, so text order is time order.
CREATE TABLE IF NOT EXISTS incident_transitions (
    provider_id TEXT NOT NULL,
    indicator TEXT NOT NULL,
    description TEXT NOT NULL,
    observed_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS incident_transitions_provider_time
    ON incident_transitions (provider_id, observed_at);
