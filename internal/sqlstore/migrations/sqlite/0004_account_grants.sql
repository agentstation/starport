-- An account grant maps who may use which account: it gives one account
-- to one user or one team. Exactly one grantee side is set; the other is
-- stored as the empty string so the whole row can be the primary key,
-- which is also the uniqueness rule — the same grant stated twice is a
-- conflict, not a second row.
CREATE TABLE IF NOT EXISTS account_grants (
    account_id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    team_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (account_id, user_id, team_id)
);
