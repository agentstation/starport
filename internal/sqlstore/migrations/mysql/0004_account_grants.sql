-- An account grant maps who may use which account: it gives one account
-- to one user or one team. Exactly one grantee side is set; the other is
-- stored as the empty string so the whole row can be the primary key,
-- which is also the uniqueness rule — the same grant stated twice is a
-- conflict, not a second row.
--
-- MySQL auto-commits DDL, so this file stays idempotent-safe: IF NOT
-- EXISTS lets a retried migration pass. Key columns are bounded at 191
-- because MySQL cannot index an unbounded TEXT column; three 191-char
-- utf8mb4 columns total 2292 bytes, inside InnoDB's 3072-byte index cap.
CREATE TABLE IF NOT EXISTS account_grants (
    account_id VARCHAR(191) NOT NULL,
    user_id VARCHAR(191) NOT NULL DEFAULT '',
    team_id VARCHAR(191) NOT NULL DEFAULT '',
    created_at VARCHAR(64) NOT NULL,
    PRIMARY KEY (account_id, user_id, team_id)
);
