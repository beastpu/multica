-- Dropping workflow.status took two indexes with it, because both were defined
-- over that column: the live-name uniqueness (partial, WHERE status <>
-- 'archived') and idx_workflow_workspace_status. Postgres drops dependent
-- indexes silently, so this restores what the column removal was not meant to
-- take with it.
--
-- Uniqueness is now unconditional. It used to exempt archived rows so a retired
-- workflow's name could be reused; with archiving gone the only way to free a
-- name is to delete the workflow, which frees it for real. The expression stays
-- lower(btrim(name)): casing and padding must not buy a second copy, because
-- they are invisible to whoever reads the list.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS workflow_live_name_idx
    ON workflow (workspace_id, lower(btrim(name)));
