-- An established team never gains a fresh zero-history grant through migration.
-- Rows survive team deletion. Recovery must preserve them with budget KV state.
CREATE TABLE IF NOT EXISTS team_budget_origins (
    team_id VARCHAR(191) PRIMARY KEY,
    history_id VARCHAR(256) NOT NULL,
    initialize_allowed INTEGER NOT NULL DEFAULT 0
);
INSERT INTO team_budget_origins (team_id, history_id, initialize_allowed)
SELECT id, '', 0 FROM teams
WHERE NOT EXISTS (SELECT 1 FROM team_budget_origins WHERE team_id = teams.id);
