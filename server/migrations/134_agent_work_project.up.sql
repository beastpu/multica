-- Agent derived-work ("agent_work") system projects.
--
-- Each derived-work kind (p4_assessment, connectivity_check, ...) owns one
-- lazily created system project per workspace, used purely for organizing the
-- projection issues (docs/agent-fix-p4-assessment-issue-design.md, Part 1).
-- Guards / isolation / queries always key on the issue's reserved
-- metadata.agent_work marker, never on project membership — the project can
-- be renamed or moved, the metadata is fixed at creation.
--
-- PRIMARY KEY (workspace_id, kind) is the idempotency key: concurrent
-- replicas that both try to lazily create the project race on this constraint
-- (INSERT ... ON CONFLICT DO NOTHING), so exactly one mapping wins.
CREATE TABLE agent_work_project (
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    kind text NOT NULL,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, kind)
);
