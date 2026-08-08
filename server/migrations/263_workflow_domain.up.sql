-- Native activity-container workflow domain.
-- Relationships are intentionally enforced in application transactions.
-- This schema has no foreign keys and no cascading actions.

CREATE TABLE workflow_template (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '',
    applies_to_kind TEXT NOT NULL DEFAULT 'issue' CHECK (applies_to_kind IN ('issue')),
    applies_to_type_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    latest_published_version_id UUID,
    created_by UUID NOT NULL,
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_template_version (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    template_id UUID NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    status TEXT NOT NULL CHECK (status IN ('draft', 'published', 'abandoned')),
    definition JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(definition) = 'object' AND pg_column_size(definition) <= 1048576),
    definition_checksum TEXT NOT NULL DEFAULT '',
    change_summary TEXT NOT NULL DEFAULT '',
    created_by UUID NOT NULL,
    published_by UUID,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_instance (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    template_id UUID NOT NULL,
    template_version_id UUID NOT NULL,
    host_issue_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('needs_setup', 'running', 'paused', 'completed', 'cancelled', 'failed')),
    host_status_mode TEXT NOT NULL CHECK (host_status_mode IN ('managed', 'independent')),
    input JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(input) = 'object' AND pg_column_size(input) <= 262144),
    result JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(result) = 'object' AND pg_column_size(result) <= 262144),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    started_by_type TEXT NOT NULL CHECK (started_by_type IN ('member', 'system')),
    started_by_id UUID,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    paused_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    last_reconciled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_instance_role_assignment (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_instance_id UUID NOT NULL,
    role_key TEXT NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent', 'squad')),
    actor_id UUID NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('fixed', 'host_assignee', 'user_selected', 'copied')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_node_instance (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_instance_id UUID NOT NULL,
    node_key TEXT NOT NULL,
    node_kind TEXT NOT NULL CHECK (node_kind IN ('activity', 'start', 'gateway', 'parallel_split', 'parallel_join', 'wait', 'end')),
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
    name_snapshot TEXT NOT NULL,
    display_order INTEGER NOT NULL DEFAULT 0,
    definition_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(definition_snapshot) = 'object' AND pg_column_size(definition_snapshot) <= 262144),
    status TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'active', 'waiting', 'completed', 'blocked', 'failed', 'skipped', 'superseded', 'cancelled')),
    waiting_reasons JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(waiting_reasons) = 'array' AND pg_column_size(waiting_reasons) <= 65536),
    latest_submission_id UUID,
    latest_verdict_id UUID,
    activated_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    superseded_at TIMESTAMPTZ,
    last_reconciled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_node_participant (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_node_instance_id UUID NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('owner', 'participant', 'approver')),
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent', 'squad')),
    actor_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_executor_resolution (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_instance_id UUID NOT NULL,
    workflow_node_instance_id UUID NOT NULL,
    workflow_node_task_id UUID,
    strategy TEXT NOT NULL CHECK (strategy IN ('fixed_role', 'previous_selected', 'capability_match', 'fallback_role', 'manual')),
    status TEXT NOT NULL CHECK (status IN ('resolved', 'needs_setup', 'failed')),
    actor_type TEXT CHECK (actor_type IN ('member', 'agent', 'squad')),
    actor_id UUID,
    candidates JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(candidates) = 'array' AND pg_column_size(candidates) <= 65536),
    reason TEXT NOT NULL DEFAULT '',
    definition_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(definition_snapshot) = 'object' AND pg_column_size(definition_snapshot) <= 65536),
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'resolved') = (actor_type IS NOT NULL AND actor_id IS NOT NULL))
);

CREATE TABLE workflow_node_task (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_instance_id UUID NOT NULL,
    workflow_node_instance_id UUID NOT NULL,
    task_key TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('template', 'dynamic')),
    required BOOLEAN NOT NULL DEFAULT false,
    definition_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(definition_snapshot) = 'object' AND pg_column_size(definition_snapshot) <= 262144),
    materialization_status TEXT NOT NULL CHECK (materialization_status IN ('pending_materialization', 'materializing', 'materialized', 'failed', 'cancelled')),
    issue_id UUID,
    executor_resolution_id UUID,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    claimed_at TIMESTAMPTZ,
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent', 'squad', 'system')),
    created_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_node_submission (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_instance_id UUID NOT NULL,
    workflow_node_instance_id UUID NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    status TEXT NOT NULL CHECK (status IN ('valid', 'invalid', 'superseded')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(payload) = 'object' AND pg_column_size(payload) <= 262144),
    summary TEXT NOT NULL DEFAULT '',
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(evidence) = 'array' AND pg_column_size(evidence) <= 262144),
    submitted_by_type TEXT NOT NULL CHECK (submitted_by_type IN ('member', 'agent', 'squad', 'system')),
    submitted_by_id UUID,
    source_issue_id UUID,
    source_agent_run_id UUID,
    schema_version INTEGER NOT NULL DEFAULT 1 CHECK (schema_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_node_verdict (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_instance_id UUID NOT NULL,
    workflow_node_instance_id UUID NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    result TEXT NOT NULL CHECK (result IN ('pass', 'fail', 'blocked')),
    reason TEXT NOT NULL DEFAULT '',
    confidence DOUBLE PRECISION CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(evidence) = 'array' AND pg_column_size(evidence) <= 262144),
    basis JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(basis) = 'object' AND pg_column_size(basis) <= 262144),
    evaluator_type TEXT NOT NULL CHECK (evaluator_type IN ('deterministic', 'member', 'agent')),
    evaluator_id UUID,
    definition_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(definition_snapshot) = 'object' AND pg_column_size(definition_snapshot) <= 65536),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_node_confirmation (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_node_instance_id UUID NOT NULL,
    member_id UUID NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    comment TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workflow_acceptance (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_instance_id UUID NOT NULL,
    workflow_node_instance_id UUID NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'changes_requested', 'blocked')),
    decided_by_type TEXT CHECK (decided_by_type IN ('member', 'system')),
    decided_by_id UUID,
    reason TEXT NOT NULL DEFAULT '',
    rework_target_node_key TEXT,
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(evidence) = 'array' AND pg_column_size(evidence) <= 262144),
    idempotency_key TEXT NOT NULL,
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'pending') OR decided_by_type IS NOT NULL),
    CHECK ((status NOT IN ('rejected', 'changes_requested')) OR (reason <> '' AND rework_target_node_key IS NOT NULL))
);

CREATE TABLE workflow_event (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_instance_id UUID NOT NULL,
    workflow_node_instance_id UUID,
    event_type TEXT NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent', 'squad', 'system')),
    actor_id UUID,
    idempotency_key TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(payload) = 'object' AND pg_column_size(payload) <= 262144),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_origin_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_origin_type_check
    CHECK (origin_type IN ('autopilot', 'quick_create', 'lark_chat', 'slack_chat', 'agent_create', 'workflow'));
