-- Identity owns the humans a deployment knows. A user carries the
-- provider-qualified external subject an acquisition path resolves, a
-- team names a group, and a membership ties one user to one team. Users
-- and teams travel as one JSON record beside their addressed columns and
-- an optimistic-concurrency revision; a membership is a bare link row.
--
-- MySQL auto-commits DDL, so this file stays idempotent-safe: IF NOT
-- EXISTS lets a retried migration pass. Indexed ids are bounded because
-- MySQL cannot index an unbounded TEXT column, and 191 keeps each index
-- inside utf8mb4's 767-byte limit.
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(191) PRIMARY KEY,
    subject VARCHAR(191) NOT NULL UNIQUE,
    revision BIGINT NOT NULL,
    record TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS teams (
    id VARCHAR(191) PRIMARY KEY,
    revision BIGINT NOT NULL,
    record TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS team_memberships (
    user_id VARCHAR(191) NOT NULL,
    team_id VARCHAR(191) NOT NULL,
    created_at VARCHAR(64) NOT NULL,
    PRIMARY KEY (user_id, team_id)
);
