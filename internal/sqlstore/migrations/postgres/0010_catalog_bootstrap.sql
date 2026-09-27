ALTER TABLE catalog_recovery ADD COLUMN bootstrap_allowed INTEGER NOT NULL DEFAULT 0 CHECK (bootstrap_allowed IN (0, 1));
