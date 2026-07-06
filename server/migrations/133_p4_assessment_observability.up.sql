-- Assessment queue observability + the phase-2 projection hook.
--
-- attempt_count / last_error answer "why is this row stuck" without psql:
-- every lease increments the counter, evidence-release and task-failure record
-- the reason (the release path used to be fully silent), completion clears it.
-- assessment_issue_id is the derived-work issue for the CURRENT run
-- (docs/agent-fix-p4-assessment-issue-design.md); it stays NULL until the
-- projection phase lands and re-points on every force re-run.
ALTER TABLE agent_fix_p4_assessment
    ADD COLUMN assessment_issue_id uuid REFERENCES issue(id) ON DELETE SET NULL,
    ADD COLUMN attempt_count integer NOT NULL DEFAULT 0,
    ADD COLUMN last_error text NOT NULL DEFAULT '';
