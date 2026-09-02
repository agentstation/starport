-- The request that carried an admin mutation joins the audit trail to the
-- usage listing and the request log. An empty value marks a write that
-- reached the store without a request context. The default keeps every
-- row the table already holds valid without a rewrite.
--
-- MySQL auto-commits DDL and has no ADD COLUMN IF NOT EXISTS. A migration
-- that fails between this statement and its schema_migrations row reports
-- a duplicate column on the retry. Drop the column or record the row by
-- hand, then start the gateway again.
ALTER TABLE audit_log ADD COLUMN request_id VARCHAR(191) NOT NULL DEFAULT '';
