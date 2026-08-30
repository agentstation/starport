-- An audit record is one admin mutation this gateway performed: who asked
-- (a key name, a console grant kind, or an identity user), what they did,
-- what it touched, and how it ended. It never holds a credential value.
-- Timestamps are RFC 3339 UTC strings, so text order is time order. The
-- integer id orders records within one instant and carries the paging
-- cursor.
CREATE TABLE IF NOT EXISTS audit_log (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TEXT NOT NULL,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    subject TEXT NOT NULL,
    outcome TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS audit_log_time ON audit_log (occurred_at);
