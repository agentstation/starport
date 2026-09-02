-- The request that carried an admin mutation joins the audit trail to the
-- usage listing and the request log. An empty value marks a write that
-- reached the store without a request context. The default keeps every
-- row the table already holds valid without a rewrite.
ALTER TABLE audit_log ADD COLUMN request_id TEXT NOT NULL DEFAULT '';
