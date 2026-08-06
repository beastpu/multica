-- name: ListAgents :many
SELECT * FROM agent
WHERE workspace_id = $1 AND archived_at IS NULL AND kind = 'user'
ORDER BY created_at ASC;

-- name: ListAllAgents :many
SELECT * FROM agent
WHERE workspace_id = $1 AND kind = 'user'
ORDER BY created_at ASC;

-- name: ListAgentIssueDailySummaries :many
WITH task_stats AS (
    SELECT
        agent_id,
        count(*) FILTER (
            WHERE status = 'completed'
              AND completed_at >= sqlc.arg('window_start')::timestamptz
              AND completed_at < sqlc.arg('window_end')::timestamptz
        )::int AS tasks_completed,
        count(*) FILTER (
            WHERE status = 'failed'
              AND completed_at >= sqlc.arg('window_start')::timestamptz
              AND completed_at < sqlc.arg('window_end')::timestamptz
        )::int AS tasks_failed,
        count(*) FILTER (
            WHERE created_at >= sqlc.arg('window_start')::timestamptz
              AND created_at < sqlc.arg('window_end')::timestamptz
        )::int AS tasks_started
    FROM agent_task_queue
    WHERE created_at < sqlc.arg('window_end')::timestamptz
      AND (
          created_at >= sqlc.arg('window_start')::timestamptz
          OR completed_at >= sqlc.arg('window_start')::timestamptz
      )
    GROUP BY agent_id
),
issue_stats AS (
    SELECT
        assignee_id AS agent_id,
        count(*) FILTER (WHERE status = 'backlog')::int AS backlog_count,
        count(*) FILTER (WHERE status = 'todo')::int AS todo_count,
        count(*) FILTER (WHERE status = 'in_progress')::int AS in_progress_count,
        count(*) FILTER (WHERE status = 'in_review')::int AS in_review_count,
        count(*) FILTER (WHERE status = 'blocked')::int AS blocked_count,
        count(*) FILTER (WHERE status NOT IN ('done', 'cancelled'))::int AS active_count,
        count(*) FILTER (
            WHERE status IN ('done', 'cancelled')
              AND updated_at >= sqlc.arg('window_start')::timestamptz
              AND updated_at < sqlc.arg('window_end')::timestamptz
        )::int AS closed_in_window
    FROM issue
    WHERE assignee_type = 'agent'
    GROUP BY assignee_id
)
SELECT
    a.workspace_id,
    a.id AS agent_id,
    a.name AS agent_name,
    a.owner_id,
    COALESCE(i.backlog_count, 0)::int AS backlog_count,
    COALESCE(i.todo_count, 0)::int AS todo_count,
    COALESCE(i.in_progress_count, 0)::int AS in_progress_count,
    COALESCE(i.in_review_count, 0)::int AS in_review_count,
    COALESCE(i.blocked_count, 0)::int AS blocked_count,
    COALESCE(i.active_count, 0)::int AS active_count,
    COALESCE(i.closed_in_window, 0)::int AS closed_in_window,
    COALESCE(t.tasks_started, 0)::int AS tasks_started,
    COALESCE(t.tasks_completed, 0)::int AS tasks_completed,
    COALESCE(t.tasks_failed, 0)::int AS tasks_failed
FROM agent a
LEFT JOIN issue_stats i ON i.agent_id = a.id
LEFT JOIN task_stats t ON t.agent_id = a.id
WHERE a.archived_at IS NULL
  AND a.owner_id IS NOT NULL
  AND (
      COALESCE(i.active_count, 0) > 0
      OR COALESCE(i.closed_in_window, 0) > 0
      OR COALESCE(t.tasks_started, 0) > 0
      OR COALESCE(t.tasks_completed, 0) > 0
      OR COALESCE(t.tasks_failed, 0) > 0
  )
ORDER BY a.workspace_id, a.name ASC;

-- name: GetAgent :one
SELECT * FROM agent
WHERE id = $1;

-- name: GetAgentForUpdate :one
-- Serializes read-modify-write updates to disabled_runtime_skills so two
-- concurrent per-skill toggles cannot overwrite each other.
SELECT * FROM agent
WHERE id = $1
FOR UPDATE;

-- name: GetAgentInWorkspace :one
SELECT * FROM agent
WHERE id = $1 AND workspace_id = $2 AND kind = 'user';

-- name: GetFirstAgentByOwnerInWorkspace :one
SELECT * FROM agent
WHERE workspace_id = $1 AND owner_id = $2 AND archived_at IS NULL
ORDER BY created_at ASC
LIMIT 1;

-- name: CreateAgent :one
INSERT INTO agent (
    workspace_id, name, description, avatar_url, runtime_mode,
    runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id,
    instructions, custom_env, custom_args, mcp_config, model, thinking_level,
    service_tier,
    composio_toolkit_allowlist, permission_mode
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16,
    $17,
    sqlc.narg('composio_toolkit_allowlist')::text[],
    COALESCE(sqlc.narg('permission_mode'), 'private')
)
RETURNING *;

-- name: CreateAgentBuilder :one
-- One hidden builder agent per creation session. Keeping the execution carrier
-- session-scoped freezes its model/runtime configuration when multiple builder
-- flows are open concurrently, while `kind = 'system'` keeps it out of normal
-- agent lists and assignment surfaces.
INSERT INTO agent (
    workspace_id, name, description, runtime_mode, runtime_config, runtime_id,
    visibility, permission_mode, max_concurrent_tasks, owner_id, instructions,
    custom_env, custom_args, model, kind, system_key
) VALUES (
    @workspace_id, @name, '', @runtime_mode, '{}'::jsonb, @runtime_id,
    'private', 'private', 1, @owner_id, @instructions,
    '{}'::jsonb, '[]'::jsonb, sqlc.narg('model'), 'system', @system_key
)
RETURNING *;

-- name: DeleteSystemAgentByID :exec
-- Builder sessions own their hidden execution agent. Deleting the session
-- removes that carrier and its task rows; the kind guard prevents this cleanup
-- path from ever deleting a user-authored agent.
DELETE FROM agent
WHERE id = $1 AND kind = 'system' AND system_key LIKE 'agent_builder:%';

-- name: RebindAgentBuilderRuntime :one
-- Re-points a builder carrier at another runtime mid-conversation. The carrier
-- is what SendDirectChatMessage reads to stamp a chat task's runtime_id, so this
-- UPDATE is the only thing that actually moves subsequent replies; the live-draft
-- picker alone never did. model is reset wholesale because model ids are
-- per-runtime — the new runtime resolves its own default instead of inheriting an
-- id it may not serve. The kind/system_key guard mirrors DeleteSystemAgentByID so
-- this path can never touch a user-authored agent.
--
-- Callers MUST hold LockChatSessionForRuntimeBind on the owning chat_session for
-- the whole transaction, otherwise a concurrent send can stamp a task with the
-- pre-switch runtime after the pending-task check has already passed (MUL-5163).
--
-- chat_session.runtime_id is deliberately left untouched: the daemon only resumes
-- a stored provider session when chat_session.runtime_id matches the claiming
-- task's runtime (see the chat claim path), so leaving the old pointer in place is
-- exactly what makes the new runtime start a fresh provider session.
UPDATE agent
SET runtime_id = @runtime_id,
    runtime_mode = @runtime_mode,
    model = sqlc.narg('model'),
    updated_at = now()
WHERE id = @id AND kind = 'system' AND system_key LIKE 'agent_builder:%'
RETURNING *;

-- name: UpdateAgent :one
-- composio_toolkit_allowlist is set wholesale: the API layer is responsible
-- for normalising the request payload to either (a) the new slug list — sent
-- here verbatim — or (b) an empty array to explicitly disable Composio.
-- Distinguish "field omitted" (preserve) from "explicit clear" via
-- ClearAgentComposioToolkitAllowlist below, mirroring the
-- thinking_level / mcp_config two-query pattern: COALESCE can't restore NULL.
UPDATE agent SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    avatar_url = COALESCE(sqlc.narg('avatar_url'), avatar_url),
    runtime_config = COALESCE(sqlc.narg('runtime_config'), runtime_config),
    runtime_mode = COALESCE(sqlc.narg('runtime_mode'), runtime_mode),
    runtime_id = COALESCE(sqlc.narg('runtime_id'), runtime_id),
    visibility = COALESCE(sqlc.narg('visibility'), visibility),
    permission_mode = COALESCE(sqlc.narg('permission_mode'), permission_mode),
    status = COALESCE(sqlc.narg('status'), status),
    max_concurrent_tasks = COALESCE(sqlc.narg('max_concurrent_tasks'), max_concurrent_tasks),
    instructions = COALESCE(sqlc.narg('instructions'), instructions),
    custom_env = COALESCE(sqlc.narg('custom_env'), custom_env),
    custom_args = COALESCE(sqlc.narg('custom_args'), custom_args),
    mcp_config = COALESCE(sqlc.narg('mcp_config'), mcp_config),
    model = COALESCE(sqlc.narg('model'), model),
    thinking_level = COALESCE(sqlc.narg('thinking_level'), thinking_level),
    service_tier = COALESCE(sqlc.narg('service_tier'), service_tier),
    composio_toolkit_allowlist = COALESCE(sqlc.narg('composio_toolkit_allowlist')::text[], composio_toolkit_allowlist),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearAgentComposioToolkitAllowlist :one
-- Explicit NULL-clear for composio_toolkit_allowlist. The COALESCE-based
-- UpdateAgent cannot set the column back to NULL — sending an empty array
-- through there would persist `{}` (still a non-NULL, equivalent to "no
-- toolkits" but distinct from "field never configured"). The API uses this
-- dedicated query when the agent owner removes every toolkit; subsequent
-- dispatch decisions treat NULL identically to `{}` (both -> no overlay).
UPDATE agent SET composio_toolkit_allowlist = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearAgentThinkingLevel :one
-- Explicit NULL-clear for thinking_level. COALESCE-based UpdateAgent cannot
-- set the column back to NULL, so the API layer routes "user picked Default"
-- through this dedicated query.
UPDATE agent SET thinking_level = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearAgentServiceTier :one
-- Explicit NULL-clear for service_tier. COALESCE-based UpdateAgent cannot
-- set the column back to NULL, so the API routes "Runtime default" here.
UPDATE agent SET service_tier = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearAgentMcpConfig :one
UPDATE agent SET mcp_config = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateAgentCustomEnv :one
-- Replaces an agent's custom_env map wholesale. Used by the dedicated
-- env-management endpoint (POST/PUT /api/agents/{id}/env), which is the
-- only post-creation write path for env. UpdateAgent has been stripped
-- of custom_env handling so all env mutations flow through here and the
-- handler's audit-log + **** sentinel guard.
UPDATE agent
SET custom_env = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateAgentDisabledRuntimeSkills :one
UPDATE agent
SET disabled_runtime_skills = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ArchiveAgent :one
UPDATE agent SET archived_at = now(), archived_by = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ArchiveAgentsByRuntime :many
-- Bulk-archives every active agent bound to any runtime in the given set.
-- Used when revoking a leaving member's runtimes so agents pinned to those
-- runtimes can no longer be assigned new work. Returns the affected rows so
-- the caller can broadcast agent:archived per agent.
UPDATE agent
SET archived_at = now(), archived_by = @archived_by, updated_at = now()
WHERE runtime_id = ANY(@runtime_ids::uuid[]) AND archived_at IS NULL
RETURNING *;

-- name: ArchiveAgentsByIDs :many
-- Narrow archive that only touches the explicit ID list. Used by the
-- cascade-delete endpoint so the user's expected_active_agent_ids list
-- is the authoritative bound on what gets archived: any agent that
-- appeared on the runtime after the user opened the dialog is filtered
-- out here so it can't be silently archived even in the (vanishingly
-- rare) case where a row-level race slips past the runtime FOR UPDATE
-- lock. Returns the affected rows so the caller can broadcast
-- agent:archived per agent.
UPDATE agent
SET archived_at = now(), archived_by = @archived_by, updated_at = now()
WHERE id = ANY(@agent_ids::uuid[]) AND archived_at IS NULL
RETURNING *;

-- name: ListActiveAgentsByRuntime :many
-- Returns every non-archived agent bound to a runtime. Backs the cascade
-- delete dialog: when DELETE /api/runtimes/:id refuses with
-- runtime_has_active_agents, the response carries this list so the front-end
-- can render exactly the agents that will be archived if the user confirms,
-- and so the cascade endpoint's expected_active_agent_ids check has a stable
-- snapshot to compare against. Ordered by name for a deterministic display.
SELECT * FROM agent
WHERE runtime_id = $1 AND archived_at IS NULL AND kind = 'user'
ORDER BY name ASC;

-- name: ListActiveAgentsByRuntimeForUpdate :many
-- FOR UPDATE variant used inside the cascade-delete transaction. Locks
-- each currently-active agent row so a concurrent archive/move of one
-- of those rows blocks until our transaction commits. Pair with
-- LockAgentRuntime, which holds the runtime row exclusively to also
-- block FK-validated INSERTs / runtime_id updates that would otherwise
-- add a new agent to the runtime mid-cascade. Together they guarantee
-- that the set we compared against expected_active_agent_ids is exactly
-- the set ArchiveAgentsByIDs will operate on — no race window.
SELECT * FROM agent
WHERE runtime_id = $1 AND archived_at IS NULL AND kind = 'user'
ORDER BY name ASC
FOR UPDATE;

-- name: RestoreAgent :one
UPDATE agent SET archived_at = NULL, archived_by = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListAgentTasks :many
SELECT * FROM agent_task_queue
WHERE agent_id = $1
ORDER BY created_at DESC;

-- name: CreateAgentTask :one
-- head_sha stamps the commit under review into the task's context JSONB so the
-- reviewer-loop dedup (HasPendingTaskForIssueAndAgent) can tell a pending run
-- against an OLD head apart from a fresh request against a NEW head (TEN-356).
-- Empty/absent head_sha leaves context NULL, preserving pre-TEN-356 behavior for
-- issues with no linked PR. Issue-linked tasks never hit quick-create context
-- parsing (parseQuickCreateContext short-circuits on IssueID.Valid), so this
-- key rides harmlessly alongside.
INSERT INTO agent_task_queue (
    agent_id, runtime_id, issue_id, status, priority, trigger_comment_id,
    coalesced_comment_ids, trigger_summary, force_fresh_session, is_leader_task, handoff_note,
    squad_id, context, originator_user_id, accountable_user_id, runtime_mcp_overlay, runtime_connected_apps,
    originator_source, delegated_from_task_id, rule_version_id, rerun_of_task_id, trigger_evidence_kind, trigger_evidence_ref_id
)
VALUES (
    $1, $2, $3, 'queued', $4, sqlc.narg(trigger_comment_id),
    COALESCE(sqlc.narg(coalesced_comment_ids)::uuid[], '{}'),
    sqlc.narg(trigger_summary),
    COALESCE(sqlc.narg('force_fresh_session')::boolean, FALSE),
    COALESCE(sqlc.narg('is_leader_task')::boolean, FALSE),
    sqlc.narg(handoff_note),
    sqlc.narg(squad_id),
    CASE
        WHEN COALESCE(sqlc.narg('head_sha')::text, '') <> ''
        THEN jsonb_build_object('head_sha', sqlc.narg('head_sha')::text)
        ELSE NULL
    END,
    sqlc.narg(originator_user_id),
    sqlc.narg(accountable_user_id),
    sqlc.narg(runtime_mcp_overlay),
    sqlc.narg(runtime_connected_apps),
    sqlc.narg(originator_source),
    sqlc.narg(delegated_from_task_id),
    sqlc.narg(rule_version_id),
    sqlc.narg(rerun_of_task_id),
    sqlc.narg(trigger_evidence_kind),
    sqlc.narg(trigger_evidence_ref_id)
)
RETURNING *;

-- name: CreateQuickCreateTask :one
-- Quick-create tasks have no issue / chat / autopilot link; the entire job
-- description (prompt, requester, workspace) lives in context JSONB. The
-- daemon detects this variant via context.type == "quick_create".
-- The requester who opened the quick-create modal is a direct_human originator
-- and accountable; attribution provenance is stamped so this path is not a
-- NULL-source enqueue bypass (MUL-4302 §2).
INSERT INTO agent_task_queue (
    agent_id, runtime_id, issue_id, status, priority, context, originator_user_id,
    accountable_user_id, runtime_mcp_overlay, runtime_connected_apps,
    originator_source, trigger_evidence_kind, trigger_evidence_ref_id
)
VALUES (
    $1, $2, NULL, 'queued', $3, $4,
    sqlc.narg(originator_user_id),
    sqlc.narg(accountable_user_id),
    sqlc.narg(runtime_mcp_overlay),
    sqlc.narg(runtime_connected_apps),
    sqlc.narg(originator_source),
    sqlc.narg(trigger_evidence_kind),
    sqlc.narg(trigger_evidence_ref_id)
)
RETURNING *;

-- name: CreateWorkflowAgentTask :one
-- Issue-less workflow execution. The workflow context is self-contained and
-- workflow_node_task_id is the durable ownership link used for idempotency and
-- node readiness. A squad still executes through its leader agent while the
-- squad id remains attached for briefing and audit.
INSERT INTO agent_task_queue (
    agent_id, runtime_id, issue_id, workflow_node_task_id, status, priority,
    context, is_leader_task, squad_id, originator_user_id,
    accountable_user_id, runtime_mcp_overlay, runtime_connected_apps,
    originator_source, trigger_evidence_kind, trigger_evidence_ref_id
)
VALUES (
    @agent_id, @runtime_id, NULL, @workflow_node_task_id, 'queued', @priority,
    @context, COALESCE(sqlc.narg('is_leader_task')::boolean, FALSE),
    sqlc.narg(squad_id), sqlc.narg(originator_user_id),
    sqlc.narg(accountable_user_id), sqlc.narg(runtime_mcp_overlay),
    sqlc.narg(runtime_connected_apps), sqlc.narg(originator_source),
    'workflow_node', @workflow_node_task_id
)
RETURNING *;

-- name: GetLatestAgentTaskForWorkflowNodeTask :one
SELECT * FROM agent_task_queue
WHERE workflow_node_task_id = @workflow_node_task_id
ORDER BY attempt DESC, created_at DESC, id DESC
LIMIT 1;

-- name: GetOwningAgentTaskForWorkflowNodeTask :one
-- The run that owns a node task, in whichever shape it took: an execution the
-- task owns directly, or an agent working the task's issue. Completion asks
-- this before it reads the issue's status, because when a run did the work the
-- run's outcome is the answer — the issue is a mirror of it, and waiting for
-- someone to also move the mirror strands finished work behind a status change
-- nobody owes.
SELECT * FROM agent_task_queue
WHERE workflow_node_task_id = @workflow_node_task_id
   OR (
     sqlc.narg(issue_id)::uuid IS NOT NULL
     AND issue_id = sqlc.narg(issue_id)
     AND workflow_node_task_id IS NULL
   )
ORDER BY attempt DESC, created_at DESC, id DESC
LIMIT 1;

-- name: ListWorkflowNodeWorkerAgentTasks :many
-- A workflow activity can execute without an issue (durable task ownership)
-- or through an issue-backed task. Return both shapes from the node boundary,
-- while keeping critic executions in the review surface instead of mixing
-- them into the worker transcript history.
SELECT DISTINCT agent_task.*
FROM agent_task_queue agent_task
JOIN workflow_node_task node_task
  ON node_task.workspace_id = @workspace_id
 AND node_task.workflow_node_instance_id = @workflow_node_instance_id
 AND node_task.source <> 'critic'
 AND (
   agent_task.workflow_node_task_id = node_task.id
   OR (
     node_task.issue_id IS NOT NULL
     AND agent_task.issue_id = node_task.issue_id
     AND agent_task.workflow_node_task_id IS NULL
   )
 )
ORDER BY agent_task.created_at DESC, agent_task.id DESC
LIMIT 20;

-- name: CreateDeferredAgentTask :one
-- Deferred tasks are inert until PromoteDueDeferredTasksForRuntime flips them
-- to queued. Used for comment-routing escalation: a thread-owner primary task
-- gets a delayed assignee fallback without waking both agents at t=0.
-- Attribution is resolved and stamped at creation (not at promotion), from the
-- same trigger comment as the primary task, so the fallback assignee's run
-- carries a non-NULL source and evidence rather than bypassing attribution
-- (MUL-4302 §2).
INSERT INTO agent_task_queue (
    agent_id, runtime_id, issue_id, status, priority, trigger_comment_id,
    trigger_summary, is_leader_task, squad_id, escalation_for_task_id, fire_at,
    originator_user_id, accountable_user_id, originator_source,
    delegated_from_task_id, trigger_evidence_kind, trigger_evidence_ref_id
)
VALUES (
    @agent_id, @runtime_id, @issue_id, 'deferred', @priority,
    sqlc.narg(trigger_comment_id),
    sqlc.narg(trigger_summary),
    COALESCE(sqlc.narg('is_leader_task')::boolean, FALSE),
    sqlc.narg(squad_id),
    @escalation_for_task_id,
    @fire_at,
    sqlc.narg(originator_user_id),
    sqlc.narg(accountable_user_id),
    sqlc.narg(originator_source),
    sqlc.narg(delegated_from_task_id),
    sqlc.narg(trigger_evidence_kind),
    sqlc.narg(trigger_evidence_ref_id)
)
RETURNING *;

-- name: CreateP4AssessmentTask :one
-- Native assessment task (plan C): hangs on the assessment PROJECTION issue
-- (issue_id = the derived agent_work issue), agent_id = the workspace's
-- p4_assessment capability agent. Workflow isolation comes from the issue
-- itself: the projection issue's reserved metadata.agent_work marker anchors
-- the read-only write guard, and per-issue workflow queries never meet this
-- task through a real issue.
-- handoff_note carries the read-only assessment instructions: NEW daemons
-- build a dedicated assessment prompt from context.type and ignore it, OLD
-- daemons (pre-isolation) fall into the normal assignment path and DO render
-- handoff_note, which is how the server steers a stale daemon into read-only
-- JSON output without a client update.
INSERT INTO agent_task_queue (
    agent_id, runtime_id, issue_id, status, priority, context,
    force_fresh_session, handoff_note,
    originator_user_id, accountable_user_id, originator_source,
    trigger_evidence_kind, trigger_evidence_ref_id
)
VALUES (
    $1, $2, $3, 'queued', $4, $5, TRUE, $6,
    sqlc.narg(originator_user_id), sqlc.arg(accountable_user_id),
    sqlc.arg(originator_source), 'issue_assignment', $3
)
RETURNING *;

-- name: LinkTaskToIssue :exec
-- Attaches the issue a quick-create task produced back to the task row, once
-- the agent has finished and the issue exists. Guarded by `issue_id IS NULL`
-- so this never overwrites an issue id that was set at task creation (only
-- quick-create tasks land here unset). Fixes the activity row staying on
-- "Creating issue" forever after completion.
UPDATE agent_task_queue
SET issue_id = $2
WHERE id = $1 AND issue_id IS NULL;

-- name: CreateRetryTask :one
-- Clones a parent task into a fresh queued attempt. Carries forward the
-- agent's resume context (session_id/work_dir) so the child can continue
-- the conversation when the backend supports it. Resume-unsafe failures are
-- retried as fresh sessions so the child does not inherit a stuck agent
-- conversation. Keep the CASE WHEN predicates in sync with
-- resumeUnsafeFailureReason and the resume lookup blacklists. attempt is
-- incremented; max_attempts, trigger_comment_id, coalesced_comment_ids,
-- is_leader_task, and squad_id are inherited so the retried task receives the
-- parent's complete planned comment batch and keeps the same squad-role
-- provenance. delivered_comment_ids intentionally stays at its '{}' default:
-- the child must earn its own delivery receipt at claim time.
--
-- originator_user_id is inherited so the Composio overlay decision sees the
-- same top-of-chain human across the retry: the user behind the original
-- run has not changed. The Composio overlay follows the agent's invocation
-- permission and uses the agent owner's connection (MUL-3963); originator is
-- carried for A2A/audit, not as an originator == agent.owner_id gate.
-- A system retry is NOT a new attribution event (MUL-4302 §5): it inherits the
-- parent's accountable human, source label, delegation lineage, rule version,
-- and trigger evidence UNCHANGED, and records retry_of_task_id = p.id so retry
-- and manual rerun stay separable in reporting. parent_task_id keeps its
-- existing meaning for the retry/resume machinery; retry_of_task_id is the
-- attribution-facing lineage column.
--
-- chat_input_task_id is inherited straight from the parent so the whole retry
-- chain keeps consuming the ORIGINAL root input batch (MUL-4351): the root
-- direct task set it to its own id, every descendant copies that value, and a
-- claim always reads the same user messages. A plain copy (not
-- COALESCE(parent.chat_input_task_id, parent.id)) is deliberate: legacy/channel
-- parents carry NULL and must stay NULL so their retries keep the trailing
-- selector — promoting a pre-migration NULL row to the task-owned path on retry
-- would risk replaying untagged history during a rolling deploy.
--
-- Chat retries are queued at GREATEST(priority, 3) so a transiently-failed
-- earlier turn is re-claimed ahead of any fresh chat task (priority 2) the user
-- queued while the failing turn was still running — the retry continues the
-- older turn first. Combined with creating the retry inside FailTask's
-- transaction, this leaves no window for a newer input task to jump ahead.
--
-- fire_at arms a backoff before the retry: when non-NULL the child is inserted
-- as 'deferred' with that fire_at and stays inert until the existing
-- PromoteDueDeferredTasksForRuntime sweeper (run promote-first on every claim
-- poll) flips it to 'queued'. Used for provider_network's final attempt so it
-- waits ~5s instead of firing back-to-back with the immediate retry (MUL-4910).
-- NULL keeps the historical behaviour: an immediately-claimable 'queued' child.
--
-- max_attempts overrides the inherited budget when non-NULL (NULL inherits
-- p.max_attempts unchanged). Callers persist the reason-aware effective ceiling
-- here so the row stays self-consistent — e.g. provider_network's chain records
-- attempt=3, max_attempts=3 rather than leaking attempt=3, max_attempts=2 to the
-- task API (MUL-4910). The Go retryAttemptCeiling already refuses to raise a
-- disabled (max_attempts<=1) task, so this only ever widens, never revives.
INSERT INTO agent_task_queue (
    agent_id, runtime_id, issue_id, chat_session_id, autopilot_run_id,
    workflow_node_task_id,
    status, priority, trigger_comment_id, coalesced_comment_ids, trigger_summary, context,
    session_id, work_dir,
    attempt, max_attempts, parent_task_id, force_fresh_session, is_leader_task,
    squad_id, originator_user_id, accountable_user_id, runtime_mcp_overlay, runtime_connected_apps,
    originator_source, delegated_from_task_id, rule_version_id,
    trigger_evidence_kind, trigger_evidence_ref_id, retry_of_task_id,
    chat_input_task_id, fire_at
)
SELECT
    p.agent_id, p.runtime_id, p.issue_id, p.chat_session_id, p.autopilot_run_id,
    p.workflow_node_task_id,
    CASE WHEN sqlc.narg(fire_at)::timestamptz IS NOT NULL THEN 'deferred' ELSE 'queued' END,
    CASE WHEN p.chat_session_id IS NOT NULL THEN GREATEST(p.priority, 3) ELSE p.priority END,
    p.trigger_comment_id, p.coalesced_comment_ids, p.trigger_summary, p.context,
    CASE WHEN p.failure_reason IS NOT DISTINCT FROM 'codex_semantic_inactivity' THEN NULL ELSE p.session_id END,
    CASE WHEN p.failure_reason IS NOT DISTINCT FROM 'codex_semantic_inactivity' THEN NULL ELSE p.work_dir END,
    p.attempt + 1, COALESCE(sqlc.narg(max_attempts)::int, p.max_attempts), p.id,
    p.failure_reason IS NOT DISTINCT FROM 'codex_semantic_inactivity',
    p.is_leader_task,
    p.squad_id,
    p.originator_user_id,
    p.accountable_user_id,
    sqlc.narg(runtime_mcp_overlay),
    sqlc.narg(runtime_connected_apps),
    p.originator_source, p.delegated_from_task_id, p.rule_version_id,
    p.trigger_evidence_kind, p.trigger_evidence_ref_id, p.id,
    p.chat_input_task_id, sqlc.narg(fire_at)
FROM agent_task_queue p
WHERE p.id = $1
RETURNING *;

-- name: CancelAgentTasksByIssue :many
-- Cancels every active task on the issue and returns the affected rows so the
-- caller can reconcile each agent's status and broadcast task:cancelled events
-- (#1587). Prior :exec form silently dropped that info, leaving agents stuck at
-- status="working" with no self-correction. Only issue-deletion cleanup calls
-- this now; a status flip to cancelled/done no longer does (MUL-4465).
UPDATE agent_task_queue
SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
WHERE issue_id = $1
  AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
RETURNING *;

-- name: CancelAgentTasksByWorkflowInstance :many
-- Direct workflow executions have no issue_id, so the workflow lifecycle must
-- cancel them through their durable workflow_node_task ownership link.
UPDATE agent_task_queue agent_task
SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
FROM workflow_node_task node_task
WHERE agent_task.workflow_node_task_id = node_task.id
  AND node_task.workflow_instance_id = @workflow_instance_id
  AND node_task.workspace_id = @workspace_id
  AND agent_task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
RETURNING agent_task.*;

-- name: CancelAgentTasksByWorkflowHost :many
-- Deleting a host issue preserves the workflow runtime for audit, but active
-- direct executions still have to stop before the host relationship is cleared.
UPDATE agent_task_queue agent_task
SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
FROM workflow_node_task node_task
JOIN workflow_instance instance
  ON instance.id = node_task.workflow_instance_id
 AND instance.workspace_id = node_task.workspace_id
WHERE agent_task.workflow_node_task_id = node_task.id
  AND instance.host_issue_id = @host_issue_id
  AND instance.workspace_id = @workspace_id
  AND agent_task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
RETURNING agent_task.*;

-- name: CancelAgentTasksByIssueAndAgent :many
-- Cancels active tasks for a single (issue, agent) pair without touching
-- tasks belonging to other agents on the same issue. Used by the manual
-- rerun flow so re-running the assignee doesn't collateral-cancel a
-- still-running @-mention agent on the same issue.
UPDATE agent_task_queue
SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
WHERE issue_id = $1
  AND agent_id = $2
  AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
RETURNING *;

-- name: CancelAgentTasksByAgent :many
-- Bulk-cancel every active (queued/dispatched/running) task for an agent.
-- Returns the affected rows so callers can broadcast task:cancelled events.
-- Mirrors the shape of CancelAgentTasksByIssue / CancelAgentTasksByIssueAndAgent
-- (also :many + RETURNING + completed_at) so the three sibling cancel paths
-- behave consistently.
UPDATE agent_task_queue
SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
WHERE agent_id = $1 AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
RETURNING *;

-- name: CancelAgentTasksByTriggerComment :many
-- Cancels active tasks whose planned batch contains the edited/deleted comment.
-- The body may already have been embedded as either the primary trigger or a
-- coalesced input; cancellation prevents an agent from acting on a stale or
-- deleted version. Must run before deletion clears trigger_comment_id.
UPDATE agent_task_queue
SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
WHERE (trigger_comment_id = $1 OR $1 = ANY(coalesced_comment_ids))
  AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
RETURNING *;

-- name: CancelAgentTasksByChatSession :many
-- Cancels active tasks belonging to a chat session. Called from
-- DeleteChatSession so the daemon doesn't keep running work whose result
-- has nowhere to land. Must run BEFORE the chat_session row is deleted —
-- the FK ON DELETE SET NULL would otherwise nullify chat_session_id and we
-- could no longer reach those tasks.
UPDATE agent_task_queue
SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
WHERE chat_session_id = $1 AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
RETURNING *;

-- name: GetAgentTask :one
SELECT * FROM agent_task_queue
WHERE id = $1;

-- name: GetAgentTaskInWorkspace :one
-- Loads a task only when its owning agent lives in the given workspace.
-- agent_id is NOT NULL on every task row (and ON DELETE CASCADE, so the agent
-- always exists), which makes this the universal tenant guard for
-- user-initiated cancellation — independent of which optional source FK
-- (issue / chat_session / autopilot_run) happens to be set. It is what lets
-- run_only autopilot tasks and quick_create tasks (whose issue does not exist
-- yet) be cancelled at all, instead of 404-ing on a missing source FK.
SELECT atq.* FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE atq.id = $1 AND a.workspace_id = $2;

-- name: ClaimAgentTask :one
-- Claims the next queued task for an agent, enforcing per-(issue, agent) serialization:
-- a task is only claimable when no other task for the same issue AND same agent is
-- already dispatched or running. This allows different agents to work on the same
-- issue in parallel while preventing a single agent from running duplicate tasks.
-- Chat tasks (issue_id IS NULL) use chat_session_id for serialization instead.
-- Quick-create tasks have no issue / chat / autopilot link, so they serialize on
-- "any other quick-create-shaped task" (all four FKs NULL) for the same agent —
-- otherwise a user mashing the create button could fire concurrent quick-creates
-- whose completion lookup would race over "most recent issue by this agent".
UPDATE agent_task_queue
SET status = 'dispatched',
    dispatched_at = now(),
    prepare_lease_expires_at = now() + make_interval(secs => @prepare_lease_secs::double precision)
WHERE id = (
    SELECT atq.id FROM agent_task_queue atq
    WHERE atq.agent_id = $1 AND atq.status = 'queued'
      AND NOT EXISTS (
          SELECT 1 FROM agent_task_queue active
          WHERE active.agent_id = atq.agent_id
            AND active.status IN ('dispatched', 'running', 'waiting_local_directory')
            AND (
              (atq.issue_id IS NOT NULL
                AND active.issue_id = atq.issue_id)
              OR (atq.chat_session_id IS NOT NULL AND active.chat_session_id = atq.chat_session_id)
              OR (
                atq.issue_id IS NULL
                AND atq.chat_session_id IS NULL
                AND atq.autopilot_run_id IS NULL
                AND active.issue_id IS NULL
                AND active.chat_session_id IS NULL
                AND active.autopilot_run_id IS NULL
              )
            )
      )
    ORDER BY atq.priority DESC, atq.created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: SetTaskDeliveredCommentIDs :one
-- Replace (rather than append to) the delivery receipt for this claim. A stale
-- dispatched task may be reclaimed by a daemon with different capabilities,
-- so only the ids embedded in the newest response count as delivered. The CAS
-- keeps a stale handler from writing after execution starts, and the subset
-- guard prevents acknowledging an id outside the task's enqueue-time plan.
UPDATE agent_task_queue
SET delivered_comment_ids = @delivered_comment_ids::uuid[]
WHERE id = @task_id
  AND runtime_id = @runtime_id
  AND status = 'dispatched'
  AND started_at IS NULL
  AND dispatched_at = @dispatched_at
  AND trigger_comment_id IS NOT DISTINCT FROM sqlc.narg(expected_trigger_comment_id)::uuid
  AND NOT EXISTS (
      SELECT 1
      FROM unnest(@delivered_comment_ids::uuid[]) AS delivered(id)
      WHERE delivered.id IS NULL
         OR (
             delivered.id IS DISTINCT FROM trigger_comment_id
             AND NOT (delivered.id = ANY(coalesced_comment_ids))
         )
  )
RETURNING delivered_comment_ids;

-- name: RequeueAgentTaskAfterClaimFailure :one
-- Claim finalization (task token + optional comment receipt) failed before any
-- response bytes were written. Return only that exact claim generation to the
-- queue so another poll can retry immediately instead of waiting for stale
-- dispatch recovery. The dispatched_at CAS prevents an old handler from
-- rolling back a newer reclaim.
UPDATE agent_task_queue
SET status = 'queued',
    dispatched_at = NULL,
    prepare_lease_expires_at = NULL,
    delivered_comment_ids = '{}'
WHERE id = @task_id
  AND runtime_id = @runtime_id
  AND status = 'dispatched'
  AND started_at IS NULL
  AND dispatched_at = @dispatched_at
RETURNING *;

-- name: ReclaimStaleDispatchedTaskForRuntime :one
-- Re-delivers a task whose previous claim likely succeeded server-side but
-- whose response never reached the daemon. The task is still in `dispatched`
-- with no `started_at`, so the daemon has not acknowledged it via StartTask.
-- Refresh dispatched_at so the server-side dispatch timeout measures from the
-- recovered delivery attempt.
UPDATE agent_task_queue
SET dispatched_at = now(),
    prepare_lease_expires_at = now() + make_interval(secs => @prepare_lease_secs::double precision)
WHERE id = (
    SELECT atq.id FROM agent_task_queue atq
    WHERE atq.runtime_id = $1
      AND atq.status = 'dispatched'
      AND atq.started_at IS NULL
      AND atq.dispatched_at < now() - make_interval(secs => @claim_recovery_secs::double precision)
      AND (atq.prepare_lease_expires_at IS NULL OR atq.prepare_lease_expires_at < now())
    ORDER BY atq.priority DESC, atq.dispatched_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: ReclaimStaleDispatchedTasksForRuntimes :many
-- Batch variant of ReclaimStaleDispatchedTaskForRuntime (MUL-4257): re-delivers
-- up to @max_tasks tasks across the whole runtime set in one round trip, so a
-- machine-level batch claim recovers lost-response dispatches for every runtime
-- it hosts without one query per runtime. Same eligibility as the singular
-- query (dispatched, never started, past the recovery window, expired/absent
-- prepare lease) and the same dispatched_at refresh; only the runtime filter
-- (= ANY) and the LIMIT (max_tasks instead of 1) differ.
UPDATE agent_task_queue
SET dispatched_at = now(),
    prepare_lease_expires_at = now() + make_interval(secs => @prepare_lease_secs::double precision)
WHERE id IN (
    SELECT atq.id FROM agent_task_queue atq
    WHERE atq.runtime_id = ANY(@runtime_ids::uuid[])
      AND atq.status = 'dispatched'
      AND atq.started_at IS NULL
      AND atq.dispatched_at < now() - make_interval(secs => @claim_recovery_secs::double precision)
      AND (atq.prepare_lease_expires_at IS NULL OR atq.prepare_lease_expires_at < now())
    ORDER BY atq.priority DESC, atq.dispatched_at ASC
    LIMIT @max_tasks::int
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: ExtendAgentTaskPrepareLease :one
-- Keeps a dispatched task protected while the daemon resolves/cache/materializes
-- startup inputs before StartTask. Once the daemon stops extending this short
-- lease, the stale-dispatched reclaim path can recover the task without waiting
-- for a long global recovery window.
UPDATE agent_task_queue
SET prepare_lease_expires_at = now() + make_interval(secs => @lease_secs::double precision)
WHERE id = $1
  AND runtime_id = $2
  AND status IN ('dispatched', 'waiting_local_directory')
  AND started_at IS NULL
RETURNING *;

-- name: StartAgentTask :one
-- Transitions a task to running. Accepts either 'dispatched' (the normal
-- claim → run flow) or 'waiting_local_directory' (the daemon held the row in
-- a wait state while another task owned the local_directory path lock; once
-- the lock was acquired the daemon flips here). wait_reason is cleared on
-- the transition so a future read can't conflate "currently waiting" with
-- "previously waited".
UPDATE agent_task_queue
SET status = 'running',
    started_at = now(),
    wait_reason = NULL,
    prepare_lease_expires_at = NULL
WHERE id = $1 AND status IN ('dispatched', 'waiting_local_directory')
RETURNING *;

-- name: MarkAgentTaskWaitingLocalDirectory :one
-- Transitions a freshly-dispatched task into 'waiting_local_directory' while
-- the daemon waits for another in-flight task to release the path lock on a
-- project_resource of type local_directory. wait_reason carries a short
-- human-readable hint (typically the contested path) that the UI surfaces
-- alongside the status.
--
-- The CHECK only allows the transition from 'dispatched' so a daemon can't
-- mark an already-running or terminal task as waiting; the StartAgentTask
-- mutation handles the reverse transition once the lock is acquired.
UPDATE agent_task_queue
SET status = 'waiting_local_directory',
    wait_reason = $2,
    prepare_lease_expires_at = now() + make_interval(secs => @prepare_lease_secs::double precision)
WHERE id = $1 AND status = 'dispatched'
RETURNING *;

-- name: CompleteAgentTask :one
UPDATE agent_task_queue
SET status = 'completed', completed_at = now(), result = $2, session_id = $3, work_dir = $4, prepare_lease_expires_at = NULL
WHERE id = $1 AND status = 'running'
RETURNING *;

-- name: GetLastTaskSession :one
-- Returns the session_id and work_dir from the most recent task for a given
-- (agent_id, issue_id) pair, used for session resumption on the auto-retry
-- path. We accept both 'completed' and 'failed' tasks: a failed task may
-- have established a real agent session before crashing (orphaned by a
-- daemon restart, runtime offline, or sweeper timeout), and the daemon pins
-- the resume pointer mid-flight via UpdateAgentTaskSession. Without this,
-- an auto-retry of a mid-run failure would silently start a fresh
-- conversation and lose the in-flight context — exactly what MUL-1128's B
-- branch is meant to fix.
--
-- Manual rerun (TaskService.RerunIssue) does NOT take this path. The claim
-- handler branches on rerun_of_task_id FIRST and resolves the session/workdir
-- from that exact source task (so a parallel task on the same issue can't be
-- resumed by mistake), reusing the source workdir and resuming its session only
-- when the source failure is resume-safe. The rerun row still carries
-- force_fresh_session=true purely as a rollback-safe signal: an OLD claim
-- handler that predates the rerun_of_task_id branch falls back to this query,
-- and force_fresh_session=true makes it start clean instead of resuming the
-- wrong execution (MUL-4869).
--
-- Tasks that ended in a known "poisoned" terminal state are also excluded
-- here so even auto-retry does not inherit the bad session. The daemon
-- classifies these failures (iteration_limit, agent_fallback_message,
-- api_invalid_request, codex_semantic_inactivity, agent_error.context_overflow)
-- when it detects either an agent fallback marker in the output, an upstream
-- API 400 that means the conversation history itself is unprocessable
-- (oversized image, malformed base64, etc.), a Codex semantic inactivity
-- timeout whose recorded session may replay the same stuck state, or a context
-- window overflow that would immediately overflow again on resume. Keep this
-- list in sync with resumeUnsafeFailureReason and GetLastChatTaskSession.
--
-- The error-text ILIKE clause is defense-in-depth for the api_invalid_request
-- shape: a legacy row tagged 'agent_error' (pre-MUL-1921), a deploy-window
-- row that the old code wrote between migration and rollout, or a future
-- error format that escapes the daemon classifier all still get filtered
-- here as long as the canonical Anthropic 400 marker is present in the
-- error text. Migration 079 backfills the failure_reason column itself,
-- so observability stays accurate; this clause guarantees session resume
-- never picks up a bad session even when failure_reason hasn't caught up.
SELECT session_id, work_dir, runtime_id FROM agent_task_queue
WHERE agent_id = $1 AND issue_id = $2
  AND (
    status = 'completed'
    OR (
      status = 'failed'
      AND COALESCE(failure_reason, '') NOT IN ('iteration_limit', 'agent_fallback_message', 'api_invalid_request', 'codex_semantic_inactivity', 'agent_error.context_overflow')
      AND NOT (COALESCE(error, '') ILIKE '%400%' AND COALESCE(error, '') ILIKE '%invalid_request_error%')
    )
  )
  AND session_id IS NOT NULL
ORDER BY COALESCE(completed_at, started_at, dispatched_at, created_at) DESC
LIMIT 1;

-- name: GetLastTaskStartedAtForIssueAndAgent :one
-- Returns the started_at of the most recent prior task for this (agent, issue)
-- pair, used as the "since" anchor for counting comments that arrived since the
-- agent's last run. Any terminal state counts as "a run happened". Tasks with
-- no started_at (never dispatched / the just-claimed current task) are excluded,
-- so this never returns the current claim's own row. MUST use started_at, never
-- completed_at: a long run would otherwise miss comments posted while it ran.
SELECT started_at FROM agent_task_queue
WHERE agent_id = $1
  AND issue_id = $2
  AND started_at IS NOT NULL
ORDER BY started_at DESC
LIMIT 1;

-- name: FailAgentTask :one
-- Marks a task as failed. session_id and work_dir are merged via COALESCE so
-- if the agent already established a real session before failing (e.g. it
-- crashed mid-conversation, was cancelled, or hit a tool error) the resume
-- pointer is preserved on the task row. The next chat task can then fall
-- back to GetLastChatTaskSession and continue the conversation instead of
-- silently starting over.
--
-- failure_reason is a coarse classifier consumed by the auto-retry path;
-- 'agent_error' is the safe default when the daemon doesn't supply one.
UPDATE agent_task_queue
SET status = 'failed',
    completed_at = now(),
    error = $2,
    failure_reason = COALESCE(sqlc.narg('failure_reason'), 'agent_error'),
    session_id = COALESCE(sqlc.narg('session_id'), session_id),
    work_dir = COALESCE(sqlc.narg('work_dir'), work_dir),
    prepare_lease_expires_at = NULL
WHERE id = $1 AND status IN ('dispatched', 'running', 'waiting_local_directory')
RETURNING *;

-- name: UpdateAgentTaskSession :exec
-- Pins the resume pointer mid-flight so a daemon crash leaves a usable
-- session_id/work_dir on the task row. No-op if the task is no longer
-- in dispatched/running. waiting_local_directory tasks have no session yet
-- so this query intentionally skips them.
UPDATE agent_task_queue
SET session_id = COALESCE(sqlc.narg('session_id'), session_id),
    work_dir  = COALESCE(sqlc.narg('work_dir'), work_dir)
WHERE id = $1 AND status IN ('dispatched', 'running');

-- name: RecoverOrphanedTasksForRuntime :many
-- Called by the daemon at startup. Atomically fails any dispatched/running/
-- waiting_local_directory task that the prior incarnation of this runtime
-- owned but did not finalize. Returns the failed rows so callers can hand
-- them to the auto-retry path. waiting_local_directory rows are included
-- because the daemon holding the path lock is the same process that just
-- died — without us, the row would sit waiting forever.
UPDATE agent_task_queue
SET status = 'failed',
    completed_at = now(),
    error = 'daemon restarted while task was in flight',
    failure_reason = 'runtime_recovery',
    wait_reason = NULL,
    prepare_lease_expires_at = NULL
WHERE runtime_id = $1 AND status IN ('dispatched', 'running', 'waiting_local_directory')
RETURNING *;

-- name: FailStaleTasks :many
-- Fails tasks stuck in dispatched/running beyond the given thresholds.
--
-- Each branch pairs a wall-clock deadline with a task-appropriate liveness
-- signal, so the sweeper only kills tasks whose owning daemon is no longer
-- proving it is alive:
--
--   * Dispatched: `prepare_lease_expires_at` is refreshed every 15s by the
--     daemon between claim and StartTask (see startTaskPrepareLeaseExtender).
--     A live lease excludes the row.
--
--   * Running: no per-task lease is renewed once StartTask fires, so we key
--     off the daemon-wide heartbeat instead — `agent_runtime.last_seen_at`,
--     which the daemon bumps every ~15s while it is up. A running task whose
--     runtime is `online` AND whose `last_seen_at` is within
--     @runtime_stale_secs is treated as alive and is NOT killed by this
--     wall-clock backstop, even after `started_at` exceeds the running
--     timeout. This is what lets healthy multi-hour research / training runs
--     survive on self-hosted deployments (MUL-4107): the daemon side is
--     bounded only by inactivity watchdogs (idle / per-tool), so the
--     server-side wall clock must not shadow that with a coarser cap.
--
-- The daemon-dead case is the primary responsibility of `sweepStaleRuntimes`
-- (which mixes DB `last_seen_at` with the Redis LivenessStore and calls
-- `FailTasksForOfflineRuntimes` in the same tick). The wall-clock branch
-- here is a defensive backstop for pathological cases where a runtime row
-- somehow retains status='online' with a stale DB heartbeat for longer than
-- the wall clock allows.
--
-- runtime_id IS NULL: a running row with no runtime is by definition not
-- proving liveness, so the wall clock is allowed to fire — same shape as
-- the legacy pure-wall-clock behavior for that (rare / historical) case.
--
-- waiting_local_directory rows are intentionally excluded: the daemon owns
-- the wait (with its own ctx-driven timeout) and a legitimate queue ahead
-- of this task can exceed the dispatch / running timeouts without being
-- "stuck". If the daemon dies, RecoverOrphanedTasksForRuntime reclaims
-- those rows at restart.
UPDATE agent_task_queue
SET status = 'failed', completed_at = now(), error = 'task timed out',
    failure_reason = 'timeout',
    prepare_lease_expires_at = NULL
WHERE (
    status = 'dispatched'
    AND dispatched_at < now() - make_interval(secs => @dispatch_timeout_secs::double precision)
    AND (prepare_lease_expires_at IS NULL OR prepare_lease_expires_at < now())
  )
   OR (
    status = 'running'
    AND started_at < now() - make_interval(secs => @running_timeout_secs::double precision)
    AND NOT EXISTS (
      SELECT 1 FROM agent_runtime r
      WHERE r.id = agent_task_queue.runtime_id
        AND r.status = 'online'
        AND r.last_seen_at >= now() - make_interval(secs => @runtime_stale_secs::double precision)
    )
  )
RETURNING *;

-- name: ExpireStaleQueuedTasks :many
-- Fails tasks that have been sitting in 'queued' for longer than the TTL.
-- This is the cleanup arm of the MUL-1899 "queued backlog" fix: even with the
-- new dispatch-time admission gate that refuses to enqueue when the runtime
-- is offline, we still need to drain the historical 87k+ doomed rows and
-- handle edge cases where a runtime goes offline AFTER a task is already
-- queued (the admission check protects new enqueues, not in-flight queue
-- depth).
--
-- Concurrency safety: the daemon's claim path may race with this sweeper to
-- transition the same row out of 'queued'. We protect against that two
-- ways:
--   1. The CTE selects victims with FOR UPDATE SKIP LOCKED so a row that is
--      currently being claimed (or otherwise locked) is skipped — no lock
--      contention with the dispatch path, and we won't queue up behind it.
--   2. The outer UPDATE re-checks status='queued' AND the TTL predicate at
--      apply time. If a daemon claimed the row between selection and update
--      (e.g. lock released after the claim transaction commits), the row is
--      already 'dispatched'/'running' and the WHERE clause filters it out
--      so we cannot clobber an in-flight task.
-- Capped via LIMIT inside the CTE so a single sweep tick cannot monopolise
-- the DB when the backlog is large — the sweeper drains the rest on
-- subsequent ticks.
WITH victims AS (
    SELECT id FROM agent_task_queue
    WHERE status = 'queued'
      AND created_at < now() - make_interval(secs => @ttl_secs::double precision)
    ORDER BY created_at ASC
    LIMIT @max_per_tick::int
    FOR UPDATE SKIP LOCKED
)
UPDATE agent_task_queue t
SET status = 'failed',
    completed_at = now(),
    error = 'task expired in queue',
    failure_reason = 'queued_expired',
    prepare_lease_expires_at = NULL
FROM victims v
WHERE t.id = v.id
  AND t.status = 'queued'
  AND t.created_at < now() - make_interval(secs => @ttl_secs::double precision)
RETURNING t.*;

-- name: CancelAgentTask :one
UPDATE agent_task_queue
SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
WHERE id = $1 AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
RETURNING *;

-- name: MarkChatFinalizeDeferred :one
-- Arms the deferred chat-finalize marker for a cancelled chat task whose
-- empty-transcript judgment must wait for the daemon's flush ack (#5219).
UPDATE agent_task_queue
SET chat_finalize_deferred_at = now()
WHERE id = $1
RETURNING *;

-- name: ClaimChatFinalizeDeferred :one
-- Atomically claims the deferred marker so the daemon ack and the sweeper
-- cannot both finalize the same task (double-"Stopped." guard).
UPDATE agent_task_queue
SET chat_finalize_deferred_at = NULL
WHERE id = $1 AND chat_finalize_deferred_at IS NOT NULL
RETURNING *;

-- name: ListChatFinalizeDeferredExpired :many
-- Deferred chat finalizations whose grace period elapsed without a daemon
-- ack (dead or partitioned daemon). Batch-capped like the other sweeper
-- queries so one tick can't monopolise the DB.
SELECT * FROM agent_task_queue
WHERE chat_finalize_deferred_at IS NOT NULL
  AND chat_finalize_deferred_at < now() - make_interval(secs => @grace_secs::double precision)
ORDER BY chat_finalize_deferred_at
LIMIT @max_per_tick::int;

-- name: CountRunningTasks :one
SELECT count(*) FROM agent_task_queue
WHERE agent_id = $1 AND status IN ('dispatched', 'running', 'waiting_local_directory');

-- name: GetAgentForClaimUpdate :one
SELECT * FROM agent
WHERE id = $1
FOR UPDATE;

-- name: HasActiveTaskForIssue :one
-- Returns true if there is any queued, dispatched, waiting_local_directory,
-- or running task for the issue.
SELECT count(*) > 0 AS has_active FROM agent_task_queue
WHERE issue_id = $1
  AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory');

-- name: HasPendingTaskForIssue :one
-- Returns true if there is a queued or dispatched (but not yet running) task for the issue.
-- Used by the coalescing queue: allow enqueue when a task is running (so
-- the agent picks up new comments on the next cycle) but skip if a pending
-- task already exists (natural dedup).
SELECT count(*) > 0 AS has_pending FROM agent_task_queue
WHERE issue_id = $1
  AND status IN ('queued', 'dispatched');

-- name: HasPendingTaskForIssueAndAgent :one
-- Returns true if a specific agent already has a queued or dispatched task
-- for the given issue. Used by @mention trigger dedup.
--
-- head_sha keys the dedup on the commit under review (TEN-356): when a caller
-- passes a non-empty head_sha, a pending task only dedups if it was stamped
-- with the SAME head_sha at enqueue time. If HEAD advanced since the pending
-- task's run began (its context head_sha differs, or predates the stamp and is
-- NULL), the dedup MISSES and a fresh review enqueues against the new HEAD.
-- When head_sha is empty/NULL (issue has no linked PR) the check falls back to
-- the pre-TEN-356 (issue_id, agent_id) key so non-PR issues keep coalescing.
SELECT count(*) > 0 AS has_pending FROM agent_task_queue
WHERE issue_id = $1
  AND agent_id = $2
  AND status IN ('queued', 'dispatched')
  AND (
    COALESCE(sqlc.narg('head_sha')::text, '') = ''
    OR context->>'head_sha' = sqlc.narg('head_sha')::text
  );

-- name: HasTaskForIssueAndAgent :one
-- Returns true if a specific agent has ever had a task for the given issue.
SELECT count(*) > 0 AS has_task FROM agent_task_queue
WHERE issue_id = $1
  AND agent_id = $2;

-- name: HasPendingTaskForIssueAndAgentExcludingTriggerComment :one
-- Same as HasPendingTaskForIssueAndAgent, but ignores tasks triggered by the
-- current comment being edited. Edit preview needs this because save cancels
-- that comment's old queued/dispatched tasks before re-computing triggers.
-- Carries the same head_sha dedup key as HasPendingTaskForIssueAndAgent (TEN-356).
SELECT count(*) > 0 AS has_pending FROM agent_task_queue
WHERE issue_id = @issue_id
  AND agent_id = @agent_id
  AND status IN ('queued', 'dispatched')
  AND trigger_comment_id IS DISTINCT FROM @exclude_trigger_comment_id::uuid
  AND (
    COALESCE(sqlc.narg('head_sha')::text, '') = ''
    OR context->>'head_sha' = sqlc.narg('head_sha')::text
  );

-- name: MergeCommentIntoPendingTask :one
-- MUL-4195: fold a newly-arrived comment into an existing task for (issue,
-- agent) that has NOT yet been claimed, instead of letting the
-- HasPendingTaskForIssueAndAgent dedup silently DROP it. The task's prior
-- trigger_comment_id becomes a coalesced ("also cover") comment and
-- @new_trigger_comment_id becomes the new trigger, so the injected prompt shows
-- the latest deliberate instruction while the single run is still told to
-- address every folded comment.
--
-- Target is restricted to the single 'queued' task on purpose (MUL-4195 review
-- rounds 2–4). This merge is only reached when HasPendingTaskForIssueAndAgent
-- matched a 'queued'/'dispatched' task, and 'dispatched' is deliberately NOT a
-- target: a dispatched / waiting_local_directory / running task has already had
-- its claim response built. Folding afterward would add a planned id that is
-- absent from that response's delivered_comment_ids receipt; completion
-- reconciliation handles it instead. 'deferred' is also NOT
-- a target: a deferred row is an assignee-fallback escalation with its own
-- fire_at/promotion lifecycle, and it never sets AlreadyPending
-- (HasPendingTaskForIssueAndAgent only looks at queued/dispatched). If a newer
-- deferred fallback and an older queued task coexisted, a status-IN target would
-- pick the deferred one by created_at and steal the coalescing target away from
-- the queued run that is actually about to be claimed — so we match ONLY the
-- queued row (the idx_one_pending_task_per_issue_agent unique index guarantees
-- at most one). coalesced_comment_ids remains the pre-claim plan; the claim
-- path records the actual embedded subset in delivered_comment_ids.
--
-- Recompute-on-merge (MUL-4195 review must-fix #1): originator_user_id,
-- runtime_mcp_overlay and runtime_connected_apps are re-stamped to the NEW
-- trigger comment's originator (computed by the caller). Earlier this only
-- repointed the trigger and kept the old originator's overlay/attribution, so a
-- run answering user B's comment could execute under user A's connected-app
-- capabilities and audit identity. Re-stamping means the single coalescing run
-- carries the latest deliberate instruction's originator and the matching
-- overlay — no cross-user capability bleed, no stale attribution. This also
-- removes the previous originator gate + fresh-enqueue fallback, which could not
-- create a second task anyway (the idx_one_pending_task_per_issue_agent unique
-- index allows only one queued/dispatched task per (issue, agent)) and therefore
-- silently dropped the mismatched-originator comment.
--
-- Returns pgx.ErrNoRows when no queued task exists (it was claimed/started
-- between the dedup check and this call, or the only task is already
-- dispatched/running). The caller must NOT blindly enqueue a fresh task in that
-- case — a dispatched sibling would trip the unique index — it defers to
-- completion reconciliation unless no active task exists at all.
UPDATE agent_task_queue
SET coalesced_comment_ids = (
        SELECT COALESCE(array_agg(DISTINCT e), '{}')
        FROM unnest(array_append(coalesced_comment_ids, trigger_comment_id)) AS e
        WHERE e IS NOT NULL AND e <> @new_trigger_comment_id::uuid
    ),
    trigger_comment_id = @new_trigger_comment_id::uuid,
    trigger_summary = COALESCE(sqlc.narg('new_trigger_summary'), trigger_summary),
    -- Re-attribution is ATOMIC (MUL-4302): folding a newly-arrived comment moves the
    -- WHOLE attribution snapshot to that comment's human — person columns, source
    -- label, delegation lineage, rule version, and evidence — computed by the caller
    -- as one attribution.Result. Re-stamping only the person columns would leave a
    -- run showing B accountable while still pointing at A's stale source / evidence /
    -- level. accountable comes from the resolved Result (finalizeAttribution already
    -- guaranteed originator ⟹ accountable == originator; the cross-column CHECK backs it).
    originator_user_id = sqlc.narg('new_originator_user_id')::uuid,
    accountable_user_id = sqlc.narg('new_accountable_user_id')::uuid,
    originator_source = sqlc.narg('new_originator_source'),
    delegated_from_task_id = sqlc.narg('new_delegated_from_task_id')::uuid,
    rule_version_id = sqlc.narg('new_rule_version_id')::uuid,
    trigger_evidence_kind = sqlc.narg('new_trigger_evidence_kind'),
    trigger_evidence_ref_id = sqlc.narg('new_trigger_evidence_ref_id')::uuid,
    runtime_mcp_overlay = sqlc.narg('new_runtime_mcp_overlay'),
    runtime_connected_apps = sqlc.narg('new_runtime_connected_apps')
WHERE id = (
    SELECT t.id FROM agent_task_queue t
    WHERE t.issue_id = @issue_id
      AND t.agent_id = @agent_id
      AND t.status = 'queued'
    ORDER BY t.created_at DESC
    LIMIT 1
)
RETURNING id, coalesced_comment_ids;

-- name: HasActiveTaskForIssueAndAgent :one
-- MUL-4195: true when the (issue, agent) pair has any non-terminal task in a
-- state whose completion will run completion reconciliation — queued,
-- dispatched, running, or waiting_local_directory. Used by the comment enqueue
-- path: when a merge into a pre-claim task fails (the task is already
-- dispatched/running, or a mismatched pre-claim task exists), a fresh queued
-- INSERT would collide with idx_one_pending_task_per_issue_agent AND would risk
-- a duplicate run. Instead the caller relies on that active task's completion
-- reconcile to schedule the guaranteed follow-up, and only enqueues fresh when
-- NO active task exists.
SELECT count(*) > 0 AS has_active FROM agent_task_queue
WHERE issue_id = $1 AND agent_id = $2
  AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory');

-- name: GetLatestTaskRoleForIssueAndAgent :one
-- Returns the role markers from the agent's most recent task on this issue.
-- Used by the squad-leader self-trigger guard to tell apart leader tasks,
-- same-squad worker tasks, and generic agent tasks such as direct mentions or
-- thread-parent replies.
SELECT is_leader_task, squad_id FROM agent_task_queue
WHERE issue_id = $1 AND agent_id = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: ListPendingTasksByRuntime :many
SELECT * FROM agent_task_queue
WHERE runtime_id = $1 AND status IN ('queued', 'dispatched')
ORDER BY priority DESC, created_at ASC;

-- name: ListQueuedClaimCandidatesByRuntime :many
-- Returns rows the runtime can attempt to claim. Status is restricted to
-- 'queued' (in contrast to ListPendingTasksByRuntime which also includes
-- 'dispatched') because dispatched rows are by definition already owned
-- and cannot be re-claimed — including them in the candidate list pads
-- the result with rows that always lose the per-(issue, agent) race in
-- ClaimAgentTask, wasting CPU and a SELECT every poll cycle when the
-- runtime is busy on a long-running task. Backed by the partial index
-- idx_agent_task_queue_claim_candidates so the warm path is cheap.
SELECT * FROM agent_task_queue
WHERE runtime_id = $1 AND status = 'queued'
ORDER BY priority DESC, created_at ASC;

-- name: PromoteDueDeferredTasksForRuntime :many
UPDATE agent_task_queue
SET status = 'queued'
WHERE runtime_id = @runtime_id
  AND status = 'deferred'
  AND fire_at <= now()
RETURNING *;

-- name: ListQueuedClaimCandidatesByRuntimes :many
-- Batch variant of ListQueuedClaimCandidatesByRuntime (MUL-4257): returns
-- queued claim candidates across every runtime_id in the input set in ONE round
-- trip, so a daemon can list candidates for all of its runtimes with a single
-- query instead of one per runtime. Ordering matches the singular query
-- (priority, then FIFO) so the batch claim loop keeps the same fairness. The
-- runtime_id filter is served by the partial index
-- idx_agent_task_queue_claim_candidates; the cross-runtime ORDER BY still needs
-- a sort step (each runtime's slice is index-ordered, but merging several
-- runtimes' rows into one priority/FIFO order is not). The per-machine
-- candidate set is small, so this is cheap in practice.
SELECT * FROM agent_task_queue
WHERE runtime_id = ANY(@runtime_ids::uuid[]) AND status = 'queued'
ORDER BY priority DESC, created_at ASC;

-- name: PromoteDueDeferredTasksForRuntimes :many
-- Batch variant of PromoteDueDeferredTasksForRuntime (MUL-4257): promotes all
-- due deferred tasks across the runtime set in one UPDATE.
UPDATE agent_task_queue
SET status = 'queued'
WHERE runtime_id = ANY(@runtime_ids::uuid[])
  AND status = 'deferred'
  AND fire_at <= now()
RETURNING *;

-- name: CancelDeferredEscalationsForTask :many
UPDATE agent_task_queue
SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
WHERE escalation_for_task_id = $1
  AND status IN ('deferred', 'queued', 'dispatched', 'waiting_local_directory')
RETURNING *;

-- name: CancelDeferredEscalationsForIssueAgent :many
WITH cancelled AS (
    UPDATE agent_task_queue fallback
    SET status = 'cancelled', completed_at = now(), prepare_lease_expires_at = NULL
    FROM agent_task_queue primary_task
    WHERE fallback.escalation_for_task_id = primary_task.id
      AND fallback.status IN ('deferred', 'queued', 'dispatched', 'waiting_local_directory')
      AND primary_task.issue_id = @issue_id
      AND primary_task.agent_id = @agent_id
    RETURNING fallback.*
)
SELECT * FROM cancelled;

-- name: ListActiveTasksByIssue :many
-- Backs the issue-detail "agent live" banner. Includes 'queued' so the
-- banner shows up the moment a task is enqueued — not only after a runtime
-- claims it. The queued window can be long when the runtime is offline or
-- busy on a prior task, and a silent UI during that window looks like the
-- platform never received the trigger.
SELECT * FROM agent_task_queue
WHERE issue_id = $1 AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
ORDER BY created_at DESC;

-- name: GetWorkspaceAgentRunCounts :many
-- Total task runs per agent over the trailing 30 days, used by the Agents
-- list RUNS column. 30-day window keeps the count meaningful (a long-dormant
-- agent shouldn't show "5,420 runs from 2 years ago") and keeps the scan
-- bounded as the workspace ages.
SELECT
    atq.agent_id,
    COUNT(*)::int AS run_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = $1
  AND atq.created_at > now() - INTERVAL '30 days'
GROUP BY atq.agent_id;

-- name: GetWorkspaceAgentActivity30d :many
-- Returns per-agent daily activity buckets for the last 30 days. Single
-- workspace-wide read backs both surfaces:
--   - Agents list ACTIVITY column — uses only the trailing 7 buckets
--   - Agent detail "Last 30 days" panel — uses the full 30
-- 30 days contains 7 days, so one fetch + a client-side .slice(-7) wins
-- over fetching twice. Days with no completion produce no row; the
-- front-end zero-fills.
--
-- Anchored on completed_at (not created_at) because the sparkline answers
-- "what did this agent produce?" not "what was queued at it?". A task that's
-- still in flight has no completed_at and contributes nothing here — that's
-- correct: in-flight tasks are surfaced via the live presence indicator,
-- not the historical trend.
SELECT
    atq.agent_id,
    DATE_TRUNC('day', atq.completed_at)::timestamptz AS bucket,
    COUNT(*)::int AS task_count,
    COUNT(*) FILTER (WHERE atq.status = 'failed')::int AS failed_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = $1
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at > now() - INTERVAL '30 days'
GROUP BY atq.agent_id, bucket
ORDER BY atq.agent_id, bucket;

-- name: ListWorkspaceAgentTaskSnapshot :many
-- Returns the tasks needed to derive each agent's current presence:
--   - All active tasks (queued / dispatched / running) — for working signal + counts
--   - Each agent's most recent OUTCOME task (completed / failed) — for sticky
--     failed signal
-- The front-end picks "active wins, else latest outcome" — see derive-presence.ts.
--
-- Cancelled tasks are excluded from the outcome half on purpose: cancel is a
-- procedural signal ("attempt aborted"), not an outcome. It tells us nothing
-- about whether the agent works, so it must NOT be allowed to mask a prior
-- failure. Concretely: if an agent fails and then the user cancels the queued
-- retry (or the parent issue closes and cascades cancels), the failed signal
-- has to stay red. Only a real success (completed) or a fresh attempt (active)
-- clears it.
--
-- No UI windows in SQL: stickiness is decided by "is the latest outcome a
-- failure?", not a 2-minute clock. JOINs agent because agent_task_queue has
-- no workspace_id column.
SELECT atq.* FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = $1
  AND atq.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')

UNION ALL

SELECT t.* FROM (
  SELECT DISTINCT ON (atq.agent_id) atq.*
  FROM agent_task_queue atq
  JOIN agent a ON a.id = atq.agent_id
  WHERE a.workspace_id = $1
    AND atq.status IN ('completed', 'failed')
  ORDER BY atq.agent_id, atq.completed_at DESC NULLS LAST
) t;

-- name: ListWorkspaceAgentFixes :many
-- One row per issue that either has a recent normal agent run or a recent
-- Feishu/Meego binding. The binding spine lets Operations show every external
-- done work item, including items without an Agent assignee or normal issue
-- task. An issue may have many
-- runs (several agents, or one agent retried) — DISTINCT ON (issue_id) keeps
-- just the newest normal task (by completion, then created_at). Columns:
--   - agent_name  → who ran the latest attempt (the "智能体" column)
--   - issue_*     → the linked issue + its workflow status (the "状态" column)
--   - last_comment→ the AGENT's most recent comment/reply on the issue, the
--                   "原因/描述" column; "" when the agent left none. Member
--                   replies are intentionally excluded — this column tracks
--                   the agent's own closing action (e.g. "Review", "Summit"),
--                   which a later "收到" from a human would otherwise mask.
-- The optional `search` arg filters to issues whose agent comment contains the
-- term (case-insensitive substring). `external_status` selects the Feishu
-- business state used by Operations; when present, the SQL also requires the
-- synced Multica issue to be done. Both filters run BEFORE the 10000-row cap,
-- so metrics cover the whole reporting pool rather than an arbitrary prefix.
-- A row with
-- no matching agent comment is dropped when `search` is set.
-- JOINs agent because agent_task_queue has no workspace_id; INNER JOIN issue so
-- only issue-linked runs count. For Feishu/Meego-bound issues, the window
-- prefers Feishu's last_external_updated_at over Multica's last_synced_at so a
-- periodic sync does not make old business items look new.
-- Operations is workspace-wide; membership is enforced by the handler without
-- per-Agent visibility filtering.
WITH latest AS (
  SELECT DISTINCT ON (atq.issue_id)
    atq.id AS task_id, atq.agent_id, atq.issue_id,
    atq.started_at, atq.completed_at, atq.created_at,
    atq.status AS task_status, atq.failure_reason
  FROM agent_task_queue atq
  JOIN agent ag ON ag.id = atq.agent_id
  -- Derived agent_work issues (per-run assessment projections) are not fix
  -- targets: their tasks must not spawn feed rows or inflate the KPIs, so the
  -- spine skips issues carrying the server-reserved metadata marker.
  JOIN issue ti ON ti.id = atq.issue_id
    AND NOT jsonb_exists(ti.metadata, 'agent_work')
  WHERE ag.workspace_id = sqlc.arg('workspace_id')
    AND atq.issue_id IS NOT NULL
  -- "Latest run" = most recent activity overall: completion if finished, else
  -- start, else when it was queued. So a fresh queued/running attempt outranks
  -- an older finished one. atq.id is a final deterministic tiebreaker.
  ORDER BY atq.issue_id,
    COALESCE(atq.completed_at, atq.started_at, atq.created_at) DESC, atq.id DESC
),
spine AS (
  SELECT
    latest.task_id,
    latest.agent_id,
    latest.issue_id,
    latest.started_at,
    latest.completed_at,
    latest.created_at,
    latest.task_status,
    latest.failure_reason,
    true AS has_normal_task
  FROM latest
  LEFT JOIN feishu_project_issue_binding fib
    ON fib.issue_id = latest.issue_id
  WHERE COALESCE(
      fib.last_external_updated_at,
      latest.completed_at,
      latest.started_at,
      latest.created_at
    ) > now() - make_interval(days => sqlc.arg('days')::int)

  UNION ALL

  SELECT
    NULL::uuid AS task_id,
    CASE
      WHEN i.assignee_type = 'agent' THEN i.assignee_id
      ELSE NULL::uuid
    END AS agent_id,
    fib.issue_id,
    NULL::timestamptz AS started_at,
    NULL::timestamptz AS completed_at,
    COALESCE(fib.last_external_updated_at, fib.last_synced_at) AS created_at,
    NULL::text AS task_status,
    NULL::text AS failure_reason,
    false AS has_normal_task
  FROM feishu_project_issue_binding fib
  JOIN issue i ON i.id = fib.issue_id AND i.workspace_id = fib.workspace_id
  LEFT JOIN latest ON latest.issue_id = fib.issue_id
  WHERE fib.workspace_id = sqlc.arg('workspace_id')
    AND latest.issue_id IS NULL
    AND COALESCE(fib.last_external_updated_at, fib.last_synced_at) > now() - make_interval(days => sqlc.arg('days')::int)
)
SELECT
  spine.task_id,
  spine.agent_id,
  COALESCE(a.name, '') AS agent_name,
  i.id AS issue_id,
  i.number AS issue_number,
  i.title AS issue_title,
  i.status AS issue_status,
  COALESCE(i.assignee_type, '') AS issue_assignee_type,
  i.assignee_id AS issue_assignee_id,
  i.description AS issue_description,
  spine.started_at,
  spine.completed_at,
  spine.created_at,
  -- The latest run's own state: lets the dashboard explain a no-output row by
  -- its structured failure_reason (taskfailure taxonomy) instead of guessing.
  -- Empty for binding-only rows (no normal task).
  COALESCE(spine.task_status, '') AS task_status,
  COALESCE(spine.failure_reason, '') AS task_failure_reason,
  spine.has_normal_task,
  -- The same instant the window predicate above filters on. The dashboard
  -- splits its current/previous periods and buckets its weekly trend on this,
  -- so client-side windowing agrees with the SQL window (a ticket whose AI
  -- task ran long ago but whose external item closed this week counts as
  -- this week's activity).
  COALESCE(
    fib.last_external_updated_at,
    spine.completed_at,
    spine.started_at,
    spine.created_at
  ) AS activity_at,
  COALESCE(lc.content, '') AS last_comment,
  COALESCE(lc.author_type, '') AS last_comment_author_type,
  -- Every agent comment on the issue (member replies excluded), so the
  -- dashboard's "the agent commented a plan" signal survives a member reply
  -- and doesn't hinge on the single last_comment row above.
  (
    SELECT count(*)
    FROM comment c
    WHERE c.issue_id = i.id AND c.type = 'comment' AND c.author_type = 'agent'
  ) AS agent_comment_count,
  fib.id AS external_binding_id,
  fib.work_item_id AS external_work_item_id,
  fib.work_item_type AS external_work_item_type,
  fib.external_status_label AS external_status,
  fib.project_key AS external_project,
  fib.external_url AS external_url,
  fib.external_fields AS external_fields,
  fpi.status_mapping AS external_status_mapping,
  fpi.work_item_types AS external_work_item_types,
  p4.assessment_status AS p4_assessment_status,
  p4.delivery_attribution_prediction AS p4_delivery_attribution_prediction,
  p4.quality_prediction AS p4_quality_prediction,
  p4.prediction_reasons AS p4_prediction_reasons,
  p4.confidence AS p4_confidence,
  p4.workstream AS p4_workstream,
  p4.swarm_reviews AS p4_swarm_reviews,
  p4.ai_shelved_cls AS p4_ai_shelved_cls,
  p4.swarm_change_cls AS p4_swarm_change_cls,
  p4.swarm_committed_cls AS p4_swarm_committed_cls,
  p4.external_committed_cls AS p4_external_committed_cls,
  p4.summary AS p4_summary,
  p4.warnings AS p4_warnings,
  -- Queue observability for the detail rows: how many times this run was
  -- started, why it last failed, and which agent's task ran it.
  COALESCE(p4.attempt_count, 0) AS p4_attempt_count,
  COALESCE(p4.last_error, '') AS p4_last_error,
  COALESCE(p4agent.name, '') AS p4_assessment_agent_name,
  afr.outcome AS review_outcome,
  afr.reasons AS review_reasons,
  afr.note AS review_note,
  afr.reviewer_id AS review_reviewer_id,
  afr.reviewed_at AS review_reviewed_at
FROM spine
LEFT JOIN agent a ON a.id = spine.agent_id
JOIN issue i ON i.id = spine.issue_id
LEFT JOIN LATERAL (
  SELECT c.content, c.author_type
  FROM comment c
  WHERE c.issue_id = i.id AND c.type = 'comment' AND c.author_type = 'agent'
  ORDER BY c.created_at DESC
  LIMIT 1
) lc ON true
LEFT JOIN feishu_project_issue_binding fib
  ON fib.workspace_id = i.workspace_id AND fib.issue_id = i.id
LEFT JOIN feishu_project_integration fpi
  ON fpi.id = fib.integration_id AND fpi.workspace_id = i.workspace_id
LEFT JOIN agent_fix_p4_assessment p4
  ON p4.workspace_id = i.workspace_id AND p4.feishu_binding_id = fib.id
LEFT JOIN agent_task_queue p4task ON p4task.id = p4.assessment_task_id
LEFT JOIN agent p4agent ON p4agent.id = p4task.agent_id
LEFT JOIN agent_fix_review afr
  ON afr.workspace_id = i.workspace_id AND afr.feishu_binding_id = fib.id
WHERE
  (sqlc.narg('external_status')::text IS NULL OR (
    i.status = 'done'
    AND fib.external_status_label = sqlc.narg('external_status')::text
  ))
  AND
  -- Literal case-insensitive substring on the agent comment (no LIKE wildcard
  -- semantics, so a user-typed % or _ matches itself). NULL content (no agent
  -- comment) yields NULL > 0 → excluded, which is the desired "drop unmatched".
  (sqlc.narg('search')::text IS NULL
       OR position(lower(sqlc.narg('search')::text) IN lower(lc.content)) > 0)
ORDER BY COALESCE(fib.last_external_updated_at, spine.completed_at, spine.started_at, spine.created_at) DESC
-- The business-status filter above reduces W3's 30-day pool to roughly 4k.
-- Keep enough headroom for growth while retaining a hard response bound.
LIMIT 10000;

-- name: UpsertAgentFixReview :one
WITH binding AS (
  SELECT fib.id, fib.issue_id, fib.workspace_id
  FROM feishu_project_issue_binding fib
  WHERE fib.workspace_id = sqlc.arg('workspace_id')
    AND fib.issue_id = sqlc.arg('issue_id')
),
assessment AS (
  SELECT p4.id, p4.feishu_binding_id
  FROM agent_fix_p4_assessment p4
  JOIN binding b ON b.id = p4.feishu_binding_id
)
INSERT INTO agent_fix_review (
  workspace_id,
  issue_id,
  feishu_binding_id,
  p4_assessment_id,
  outcome,
  reasons,
  note,
  reviewer_id,
  reviewed_at,
  updated_at
)
SELECT
  b.workspace_id,
  b.issue_id,
  b.id,
  a.id,
  sqlc.arg('outcome'),
  sqlc.arg('reasons'),
  sqlc.arg('note'),
  sqlc.narg('reviewer_id'),
  CASE WHEN sqlc.arg('outcome') = 'unreviewed' THEN NULL ELSE now() END,
  now()
FROM binding b
LEFT JOIN assessment a ON a.feishu_binding_id = b.id
ON CONFLICT (workspace_id, feishu_binding_id) DO UPDATE SET
  p4_assessment_id = EXCLUDED.p4_assessment_id,
  outcome = EXCLUDED.outcome,
  reasons = EXCLUDED.reasons,
  note = EXCLUDED.note,
  reviewer_id = EXCLUDED.reviewer_id,
  reviewed_at = EXCLUDED.reviewed_at,
  updated_at = now()
RETURNING id, workspace_id, issue_id, feishu_binding_id, p4_assessment_id, outcome, reasons, note, reviewer_id, reviewed_at, created_at, updated_at;

-- name: UpsertAgentFixReviewByBinding :one
WITH binding AS (
  SELECT fib.id, fib.issue_id, fib.workspace_id
  FROM feishu_project_issue_binding fib
  WHERE fib.workspace_id = sqlc.arg('workspace_id')
    AND fib.id = sqlc.arg('feishu_binding_id')
),
assessment AS (
  SELECT p4.id, p4.feishu_binding_id
  FROM agent_fix_p4_assessment p4
  JOIN binding b ON b.id = p4.feishu_binding_id
)
INSERT INTO agent_fix_review (
  workspace_id,
  issue_id,
  feishu_binding_id,
  p4_assessment_id,
  outcome,
  reasons,
  note,
  reviewer_id,
  reviewed_at,
  updated_at
)
SELECT
  b.workspace_id,
  b.issue_id,
  b.id,
  a.id,
  sqlc.arg('outcome'),
  sqlc.arg('reasons'),
  sqlc.arg('note'),
  sqlc.narg('reviewer_id'),
  CASE WHEN sqlc.arg('outcome') = 'unreviewed' THEN NULL ELSE now() END,
  now()
FROM binding b
LEFT JOIN assessment a ON a.feishu_binding_id = b.id
ON CONFLICT (workspace_id, feishu_binding_id) DO UPDATE SET
  issue_id = EXCLUDED.issue_id,
  p4_assessment_id = EXCLUDED.p4_assessment_id,
  outcome = EXCLUDED.outcome,
  reasons = EXCLUDED.reasons,
  note = EXCLUDED.note,
  reviewer_id = EXCLUDED.reviewer_id,
  reviewed_at = EXCLUDED.reviewed_at,
  updated_at = now()
RETURNING id, workspace_id, issue_id, feishu_binding_id, p4_assessment_id, outcome, reasons, note, reviewer_id, reviewed_at, created_at, updated_at;

-- name: GetP4AssessmentBinding :one
SELECT
  fib.id AS binding_id,
  fib.workspace_id,
  fib.integration_id,
  fib.issue_id,
  fib.project_key,
  fib.work_item_type,
  fib.work_item_id,
  fib.external_identifier,
  fib.external_url,
  fib.external_status_label,
  fib.external_fields,
  i.status AS issue_status,
  i.assignee_type,
  i.assignee_id,
  i.title AS issue_title,
  i.description AS issue_description,
  i.metadata AS issue_metadata,
  a.runtime_id AS agent_runtime_id,
  a.archived_at AS agent_archived_at,
  fpi.status_mapping,
  fpi.work_item_types
FROM feishu_project_issue_binding fib
JOIN issue i ON i.id = fib.issue_id AND i.workspace_id = fib.workspace_id
JOIN feishu_project_integration fpi ON fpi.id = fib.integration_id AND fpi.workspace_id = fib.workspace_id
LEFT JOIN agent a ON a.id = i.assignee_id AND i.assignee_type = 'agent'
WHERE fib.id = $1 AND fib.workspace_id = $2;

-- name: LockP4AssessmentBinding :exec
SELECT 1
FROM feishu_project_issue_binding
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: GetP4AssessmentByBinding :one
SELECT * FROM agent_fix_p4_assessment
WHERE workspace_id = $1 AND feishu_binding_id = $2;

-- name: ListP4AssessmentBackfillBindings :many
SELECT
  fib.id AS binding_id,
  fib.workspace_id,
  fib.work_item_type,
  fib.external_status_label,
  fpi.status_mapping,
  fpi.work_item_types,
  p4.assessment_status
FROM feishu_project_issue_binding fib
JOIN feishu_project_integration fpi
  ON fpi.id = fib.integration_id AND fpi.workspace_id = fib.workspace_id
LEFT JOIN agent_fix_p4_assessment p4
  ON p4.workspace_id = fib.workspace_id AND p4.feishu_binding_id = fib.id
WHERE fib.workspace_id = $1
  AND fib.integration_id = $2
  AND p4.id IS NULL
ORDER BY fib.last_synced_at DESC, fib.created_at DESC, fib.id DESC
LIMIT $3;

-- name: UpsertP4AssessmentPending :one
INSERT INTO agent_fix_p4_assessment (
  workspace_id,
  issue_id,
  feishu_binding_id,
  assessment_status,
  prompt_version,
  updated_at
)
VALUES ($1, $2, $3, 'pending', $4, now())
ON CONFLICT (workspace_id, feishu_binding_id) DO UPDATE SET
  issue_id = EXCLUDED.issue_id,
  assessment_status = 'pending',
  assessment_task_id = NULL,
  prompt_version = EXCLUDED.prompt_version,
  updated_at = now()
RETURNING *;

-- name: CompleteP4AssessmentFromTask :one
UPDATE agent_fix_p4_assessment
SET assessment_status = 'completed',
    delivery_attribution_prediction = $3,
    quality_prediction = $4,
    prediction_reasons = $5,
    confidence = $6,
    workstream = $7,
    swarm_reviews = $8,
    ai_shelved_cls = $9,
    swarm_change_cls = $10,
    swarm_committed_cls = $11,
    external_committed_cls = $12,
    evidence = $13,
    summary = $14,
    warnings = $15,
    model = $16,
    last_error = '',
    assessed_at = now(),
    updated_at = now()
WHERE workspace_id = $1 AND assessment_task_id = $2
RETURNING *;

-- name: StartP4AssessmentFromTask :one
-- Native task flow (plan C-1): the daemon starting the assessment task
-- projects the row to running. Keyed on the task the Trigger stamped via
-- SetP4AssessmentTask; guarded against clobbering a result the agent already
-- submitted (same rationale as FailP4AssessmentFromTask). attempt_count keeps
-- counting starts so "why is this row stuck" stays answerable.
UPDATE agent_fix_p4_assessment
SET assessment_status = 'running',
    attempt_count = attempt_count + 1,
    updated_at = now()
WHERE workspace_id = $1 AND assessment_task_id = $2
  AND assessment_status <> 'completed'
RETURNING *;

-- name: SetP4AssessmentTask :one
-- Points the queue row at the native task the Trigger created for the CURRENT
-- run (plan C-1). CompleteP4AssessmentFromTask / FailP4AssessmentFromTask /
-- StartP4AssessmentFromTask and the result-submit authorization all key on
-- this column.
UPDATE agent_fix_p4_assessment
SET assessment_task_id = $3,
    updated_at = now()
WHERE workspace_id = $1 AND feishu_binding_id = $2
RETURNING *;

-- name: SetP4AssessmentIssue :exec
-- Points the queue row at the projection issue for the CURRENT run. Force
-- re-runs create a new issue and repoint; the previous issue keeps its
-- terminal state (the issue sequence is the run history).
UPDATE agent_fix_p4_assessment
SET assessment_issue_id = $3,
    updated_at = now()
WHERE workspace_id = $1 AND feishu_binding_id = $2;

-- name: FailP4AssessmentFromTask :one
-- Never clobber a result the agent already submitted through the
-- /p4-assessment/result endpoint: once the row is 'completed', a later
-- task-end parse failure must not knock it back to 'failed'. The endpoint is
-- the authoritative path; task-output parsing is only a fallback.
UPDATE agent_fix_p4_assessment
SET assessment_status = 'failed',
    warnings = $3,
    last_error = sqlc.arg(last_error),
    updated_at = now()
WHERE workspace_id = $1 AND assessment_task_id = $2
  AND assessment_status <> 'completed'
RETURNING *;

-- name: ListWorkspaceWorkingAgents :many
-- Workspace-level source for consumers that show currently working agents.
-- One row per visible, user-authored agent with at least one task that has
-- actually started running. work_type is optional (empty = every source);
-- source-specific reads use the same precedence as computeTaskKind:
-- chat > autopilot > issue. "issue" intentionally groups direct and
-- comment-triggered issue work. Quick-create work is present only in the
-- unfiltered projection because it has no source FK yet. mine_relation is
-- optional (empty = workspace); when set it narrows issue work to the
-- authenticated member's My Issues relation.
SELECT
  a.id,
  a.name,
  a.avatar_url,
  COUNT(*)::int AS running_task_count,
  COALESCE(
    ARRAY_AGG(DISTINCT atq.issue_id ORDER BY atq.issue_id)
      FILTER (WHERE atq.issue_id IS NOT NULL),
    ARRAY[]::uuid[]
  )::uuid[] AS issue_ids
FROM agent a
JOIN agent_task_queue atq ON atq.agent_id = a.id
WHERE a.workspace_id = $1
  AND a.kind = 'user'
  AND a.archived_at IS NULL
  AND atq.status = 'running'
  AND (
    @work_type::text = ''
    OR (@work_type::text = 'chat' AND atq.chat_session_id IS NOT NULL)
    OR (
      @work_type::text = 'autopilot'
      AND atq.chat_session_id IS NULL
      AND atq.autopilot_run_id IS NOT NULL
    )
    OR (
      @work_type::text = 'issue'
      AND atq.chat_session_id IS NULL
      AND atq.autopilot_run_id IS NULL
      AND atq.issue_id IS NOT NULL
    )
  )
  AND (
    @mine_relation::text = ''
    OR EXISTS (
      SELECT 1
      FROM issue i
      WHERE i.id = atq.issue_id
        AND i.workspace_id = a.workspace_id
        AND (
          (
            @mine_relation::text IN ('assigned', 'any')
            AND i.assignee_type = 'member'
            AND i.assignee_id = @member_id::uuid
          )
          OR (
            @mine_relation::text IN ('created', 'any')
            AND i.creator_type = 'member'
            AND i.creator_id = @member_id::uuid
          )
          OR (
            @mine_relation::text IN ('involved', 'any')
            AND (
              (
                i.assignee_type = 'agent'
                AND EXISTS (
                  SELECT 1
                  FROM agent owned_agent
                  WHERE owned_agent.id = i.assignee_id
                    AND owned_agent.workspace_id = a.workspace_id
                    AND owned_agent.owner_id = @member_id::uuid
                )
              )
              OR (
                i.assignee_type = 'squad'
                AND EXISTS (
                  SELECT 1
                  FROM squad s
                  WHERE s.id = i.assignee_id
                    AND s.workspace_id = a.workspace_id
                    AND (
                      EXISTS (
                        SELECT 1
                        FROM squad_member sm
                        WHERE sm.squad_id = s.id
                          AND sm.member_type = 'member'
                          AND sm.member_id = @member_id::uuid
                      )
                      OR EXISTS (
                        SELECT 1
                        FROM agent leader
                        WHERE leader.id = s.leader_id
                          AND leader.workspace_id = a.workspace_id
                          AND leader.owner_id = @member_id::uuid
                      )
                      OR EXISTS (
                        SELECT 1
                        FROM squad_member sm
                        JOIN agent owned_member ON owned_member.id = sm.member_id
                        WHERE sm.squad_id = s.id
                          AND sm.member_type = 'agent'
                          AND owned_member.workspace_id = a.workspace_id
                          AND owned_member.owner_id = @member_id::uuid
                      )
                    )
                )
              )
            )
          )
        )
    )
  )
GROUP BY a.id, a.name, a.avatar_url, a.created_at
ORDER BY a.created_at ASC;

-- name: ListTasksByIssue :many
SELECT * FROM agent_task_queue
WHERE issue_id = $1
ORDER BY created_at DESC;

-- name: ListP4EvidenceTasksByIssue :many
SELECT
  atq.id,
  atq.agent_id,
  atq.issue_id,
  atq.status,
  atq.created_at,
  atq.started_at,
  atq.completed_at,
  atq.failure_reason,
  atq.error,
  COALESCE(atq.context->>'type', '') = 'agent_fix_p4_assessment' AS is_p4_assessment
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE atq.issue_id = sqlc.arg('issue_id')
  AND a.workspace_id = sqlc.arg('workspace_id')
ORDER BY COALESCE(atq.completed_at, atq.started_at, atq.created_at) DESC, atq.id DESC
LIMIT 20;

-- name: ListP4EvidenceCommentsByIssue :many
SELECT
  c.id,
  c.author_type,
  c.author_id,
  c.type,
  c.content,
  c.source_task_id,
  c.created_at
FROM comment c
WHERE c.issue_id = sqlc.arg('issue_id')
  AND c.workspace_id = sqlc.arg('workspace_id')
  AND c.type = 'comment'
  AND (
    c.author_type = 'agent'
    OR c.content ~* '(shelved[[:space:]]+cl|swarm[[:space:]]+review|review/[0-9]+|cl[[:space:]]*[:#]?[[:space:]]*[0-9]{4,})'
  )
ORDER BY c.created_at DESC, c.id DESC
LIMIT 20;

-- name: UpdateAgentStatus :one
UPDATE agent SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RefreshAgentStatusFromTasks :one
UPDATE agent AS a
SET status = CASE WHEN EXISTS (
    SELECT 1 FROM agent_task_queue q
    WHERE q.agent_id = a.id AND q.status IN ('dispatched', 'running', 'waiting_local_directory')
) THEN 'working' ELSE 'idle' END,
    updated_at = now()
WHERE a.id = $1
RETURNING *;
