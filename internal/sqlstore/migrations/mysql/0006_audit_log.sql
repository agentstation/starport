-- An audit record is one admin mutation this gateway performed: who asked
-- (a key name, a console grant kind, or an identity user), what they did,
-- what it touched, and how it ended. It never holds a credential value.
-- Timestamps are RFC 3339 UTC strings, so text order is time order. The
-- integer id orders records within one instant and carries the paging
-- cursor.
--
-- MySQL auto-commits DDL, so this file stays idempotent-safe: IF NOT
-- EXISTS lets a retried migration pass. Indexed columns are bounded
-- because MySQL cannot index an unbounded TEXT column, and the index is
-- declared inline because MySQL has no CREATE INDEX IF NOT EXISTS.
CREATE TABLE IF NOT EXISTS audit_log (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    occurred_at VARCHAR(64) NOT NULL,
    actor VARCHAR(191) NOT NULL,
    action VARCHAR(64) NOT NULL,
    subject VARCHAR(191) NOT NULL,
    outcome VARCHAR(32) NOT NULL,
    INDEX audit_log_time (occurred_at)
);
