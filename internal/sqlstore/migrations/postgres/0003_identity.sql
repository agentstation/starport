-- Identity owns the humans a deployment knows. A user carries the
-- provider-qualified external subject an acquisition path resolves, a
-- team names a group, and a membership ties one user to one team. Users
-- and teams travel as one JSON record beside their addressed columns and
-- an optimistic-concurrency revision; a membership is a bare link row.
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL UNIQUE,
    revision BIGINT NOT NULL,
    record TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS teams (
    id TEXT PRIMARY KEY,
    revision BIGINT NOT NULL,
    record TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS team_memberships (
    user_id TEXT NOT NULL,
    team_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, team_id)
);
