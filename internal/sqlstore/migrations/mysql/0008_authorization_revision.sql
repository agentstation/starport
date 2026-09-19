CREATE TABLE IF NOT EXISTS authorization_revision (
    id INTEGER PRIMARY KEY,
    epoch VARCHAR(256) NOT NULL,
    sequence BIGINT NOT NULL
);
