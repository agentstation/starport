-- An account template names creation defaults: limits, the credential
-- strategy, the BYOK policy, and provider access. The defaults travel as
-- one JSON record beside the id and an optimistic-concurrency revision,
-- the same record shape the key-value repositories use.
--
-- MySQL auto-commits DDL, so this file stays idempotent-safe: IF NOT
-- EXISTS lets a retried migration pass. The id is bounded because MySQL
-- cannot index an unbounded TEXT primary key, and 191 keeps the index
-- inside utf8mb4's 767-byte limit.
CREATE TABLE IF NOT EXISTS account_templates (
    id VARCHAR(191) PRIMARY KEY,
    revision BIGINT NOT NULL,
    record TEXT NOT NULL
);
