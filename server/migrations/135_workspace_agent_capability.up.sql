-- Workspace capability roles (plan C-1, docs/agent-fix-p4-assessment-issue-design.md).
--
-- One row per (workspace, capability) names the agent that executes that
-- capability's derived work (first capability: 'p4_assessment'). The P4
-- assessment Trigger is fail-closed on this table: no row ⇒ no native
-- assessment task (the env allowlist P4_ASSESSMENT_WORKSPACE_ALLOWLIST
-- remains a transitional OR condition until C-2 deletes it).
--
-- project_id optionally overrides the lazily created agent_work system
-- project. max_concurrent_tasks is capability-level configuration; actual
-- claim throttling still happens through the agent's own concurrency, so this
-- column is config storage, not a second scheduler.
CREATE TABLE workspace_agent_capability (
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    capability text NOT NULL,
    agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    project_id uuid REFERENCES project(id) ON DELETE SET NULL,
    max_concurrent_tasks integer NOT NULL DEFAULT 1 CHECK (max_concurrent_tasks > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, capability)
);
