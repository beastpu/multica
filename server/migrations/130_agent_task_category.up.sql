-- task_category collapses the repeated "context->>'type' <> 'agent_fix_p4_assessment'"
-- isolation clauses into one coarse workflow-membership column:
--   'fix'      = a normal issue-fix task that participates in the issue agent
--                workflow (latest-run, session resume, dedup, cancel-on-done,
--                completion-comment fallback, leader self-trigger, etc.)
--   'analysis' = a side-channel task (currently P4 assessment) that must never
--                be treated as a fix run.
-- A new analysis task type sets 'analysis' at enqueue and needs no further query
-- changes — that is the whole point of the column. context->>'type' stays the
-- type-specific dispatch key (result routing, prompt selection, evidence flag);
-- task_category is only the workflow bucket.
ALTER TABLE agent_task_queue
    ADD COLUMN task_category TEXT NOT NULL DEFAULT 'fix'
        CHECK (task_category IN ('fix', 'analysis'));

-- Backfill existing assessment tasks (seed/demo rows, any in-flight) so the new
-- column matches exactly what the old context-based clauses computed. Rows with
-- a NULL context (every normal task) keep the 'fix' default.
UPDATE agent_task_queue
    SET task_category = 'analysis'
    WHERE context->>'type' = 'agent_fix_p4_assessment';
