-- Plan C-2 cleanup: the native task flow is the only assessment execution
-- channel, so the two batch-era columns lose their jobs.
--
-- task_category (migration 130) existed to keep batch/analysis tasks out of
-- the issue-fix workflow queries. Assessment tasks now hang on their own
-- derived agent_work projection issue, so per-issue queries never meet them
-- through a real issue, and the write guard anchors on the issue's reserved
-- metadata.agent_work marker instead of the column.
ALTER TABLE agent_task_queue DROP COLUMN task_category;

-- leased_until (migration 132) was the 30-minute batch-worker lease; execution
-- state now lives on the native task. The observability columns
-- (attempt_count / last_error / assessment_issue_id, migration 133) stay.
ALTER TABLE agent_fix_p4_assessment DROP COLUMN leased_until;
