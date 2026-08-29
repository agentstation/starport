-- An account template names creation defaults: limits, the credential
-- strategy, the BYOK policy, and provider access. The defaults travel as
-- one JSON record beside the id and an optimistic-concurrency revision,
-- the same record shape the key-value repositories use.
CREATE TABLE IF NOT EXISTS account_templates (
    id TEXT PRIMARY KEY,
    revision BIGINT NOT NULL,
    record TEXT NOT NULL
);
