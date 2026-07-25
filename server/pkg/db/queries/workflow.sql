-- =====================
-- Workflow templates
-- =====================

-- name: ListWorkflowTemplates :many
SELECT * FROM workflow_template
WHERE workspace_id = @workspace_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
ORDER BY updated_at DESC, id DESC;

-- name: ListWorkflowTemplateSummaries :many
SELECT
  template.*,
  COALESCE(published.version, 0)::integer AS latest_published_version,
  COALESCE(draft.version, 0)::integer AS draft_version,
  CAST(draft.id IS NOT NULL AS boolean) AS has_draft,
  COALESCE((
    SELECT count(*)::integer
    FROM jsonb_array_elements(
      COALESCE(published.definition, draft.definition, '{"nodes":[]}'::jsonb)
        -> 'nodes'
    ) node
    WHERE node->>'kind' = 'activity'
  ), 0)::integer AS activity_count,
  (
    SELECT count(*)::bigint
    FROM workflow_instance instance
    WHERE instance.workspace_id = template.workspace_id
      AND instance.template_id = template.id
  ) AS run_count,
  published.published_by AS last_published_by,
  published.published_at AS last_published_at,
  COALESCE(published.change_summary, draft.change_summary, '') AS latest_change_summary
FROM workflow_template template
LEFT JOIN LATERAL (
  SELECT version.*
  FROM workflow_template_version version
  WHERE version.workspace_id = template.workspace_id
    AND version.template_id = template.id
    AND version.status = 'published'
  ORDER BY version.version DESC
  LIMIT 1
) published ON true
LEFT JOIN LATERAL (
  SELECT version.*
  FROM workflow_template_version version
  WHERE version.workspace_id = template.workspace_id
    AND version.template_id = template.id
    AND version.status = 'draft'
  LIMIT 1
) draft ON true
WHERE template.workspace_id = @workspace_id
  AND (
    sqlc.narg(status)::text IS NULL
    OR template.status = sqlc.narg(status)
  )
ORDER BY template.updated_at DESC, template.id DESC;

-- name: GetWorkflowTemplateInWorkspace :one
SELECT * FROM workflow_template
WHERE id = @id AND workspace_id = @workspace_id;

-- name: CreateWorkflowTemplate :one
INSERT INTO workflow_template (
    workspace_id, name, description, applies_to_kind, applies_to_type_key,
    status, created_by
) VALUES (
    @workspace_id, @name, @description, @applies_to_kind, @applies_to_type_key,
    'draft', @created_by
)
RETURNING *;

-- name: UpdateWorkflowTemplateMetadata :one
UPDATE workflow_template
SET name = COALESCE(sqlc.narg(name), name),
    description = COALESCE(sqlc.narg(description), description),
    applies_to_type_key = COALESCE(sqlc.narg(applies_to_type_key), applies_to_type_key),
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status <> 'archived'
RETURNING *;

-- name: ArchiveWorkflowTemplate :one
UPDATE workflow_template
SET status = 'archived', archived_at = now(), updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: ListWorkflowTemplateVersions :many
SELECT * FROM workflow_template_version
WHERE template_id = @template_id AND workspace_id = @workspace_id
ORDER BY version DESC;

-- name: GetWorkflowTemplateVersionInWorkspace :one
SELECT * FROM workflow_template_version
WHERE id = @id AND workspace_id = @workspace_id;

-- name: GetWorkflowTemplateVersionByNumber :one
SELECT * FROM workflow_template_version
WHERE template_id = @template_id
  AND workspace_id = @workspace_id
  AND version = @version;

-- name: GetWorkflowTemplateDraft :one
SELECT * FROM workflow_template_version
WHERE template_id = @template_id AND workspace_id = @workspace_id AND status = 'draft'
LIMIT 1;

-- name: LockWorkflowTemplateDraft :one
SELECT * FROM workflow_template_version
WHERE template_id = @template_id AND workspace_id = @workspace_id AND status = 'draft'
LIMIT 1
FOR UPDATE;

-- name: GetLatestPublishedWorkflowTemplateVersion :one
SELECT * FROM workflow_template_version
WHERE template_id = @template_id AND workspace_id = @workspace_id AND status = 'published'
ORDER BY version DESC
LIMIT 1;

-- name: GetNextWorkflowTemplateVersion :one
SELECT COALESCE(max(version), 0)::integer + 1
FROM workflow_template_version
WHERE template_id = @template_id AND workspace_id = @workspace_id;

-- name: CreateWorkflowTemplateVersion :one
INSERT INTO workflow_template_version (
    workspace_id, template_id, version, status, definition,
    definition_checksum, change_summary, created_by
) VALUES (
    @workspace_id, @template_id, @version, @status, @definition,
    @definition_checksum, @change_summary, @created_by
)
RETURNING *;

-- name: UpdateWorkflowTemplateDraft :one
UPDATE workflow_template_version
SET definition = @definition,
    definition_checksum = @definition_checksum,
    change_summary = @change_summary,
    revision = revision + 1,
    updated_at = now()
WHERE id = @id
  AND workspace_id = @workspace_id
  AND status = 'draft'
  AND revision = @expected_revision
RETURNING *;

-- name: PublishWorkflowTemplateVersion :one
UPDATE workflow_template_version
SET status = 'published',
    published_by = @published_by,
    published_at = now(),
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'draft'
RETURNING *;

-- name: SetWorkflowTemplatePublishedVersion :one
UPDATE workflow_template
SET status = 'published',
    latest_published_version_id = @latest_published_version_id,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status <> 'archived'
RETURNING *;

-- =====================
-- Workflow runtime
-- =====================

-- name: ListIssueWorkflowContexts :many
SELECT
  task.issue_id,
  instance.id AS workflow_instance_id,
  template.id AS workflow_template_id,
  template.name AS workflow_template_name,
  node.id AS workflow_node_instance_id,
  node.node_key AS activity_key,
  node.name_snapshot AS activity_name,
  instance.host_issue_id,
  host.number AS host_issue_number,
  host.title AS host_issue_title,
  task.required
FROM workflow_node_task task
JOIN workflow_node_instance node
  ON node.id = task.workflow_node_instance_id
 AND node.workspace_id = task.workspace_id
JOIN workflow_instance instance
  ON instance.id = task.workflow_instance_id
 AND instance.workspace_id = task.workspace_id
JOIN workflow_template template
  ON template.id = instance.template_id
 AND template.workspace_id = task.workspace_id
JOIN issue host
  ON host.id = instance.host_issue_id
 AND host.workspace_id = task.workspace_id
WHERE task.workspace_id = @workspace_id
  AND task.issue_id = ANY(@issue_ids::uuid[])
ORDER BY task.created_at, task.id;

-- name: ListWorkflowInstanceDisplayContexts :many
SELECT
  wi.id AS workflow_instance_id,
  host.title AS host_issue_title,
  host.number AS host_issue_number,
  host.priority AS host_issue_priority,
  host.project_id,
  template.name AS template_name,
  version.version AS template_version,
  CAST(COALESCE((
    SELECT count(*)::integer
    FROM workflow_node_instance node
    WHERE node.workflow_instance_id = wi.id
      AND node.workspace_id = wi.workspace_id
      AND node.node_kind = 'activity'
      AND NOT EXISTS (
        SELECT 1 FROM workflow_node_instance newer
        WHERE newer.workflow_instance_id = node.workflow_instance_id
          AND newer.workspace_id = node.workspace_id
          AND newer.node_key = node.node_key
          AND newer.attempt > node.attempt
      )
  ), 0) AS integer) AS activity_total,
  CAST(COALESCE((
    SELECT count(*)::integer
    FROM workflow_node_instance node
    WHERE node.workflow_instance_id = wi.id
      AND node.workspace_id = wi.workspace_id
      AND node.node_kind = 'activity'
      AND node.status IN ('completed', 'skipped')
      AND NOT EXISTS (
        SELECT 1 FROM workflow_node_instance newer
        WHERE newer.workflow_instance_id = node.workflow_instance_id
          AND newer.workspace_id = node.workspace_id
          AND newer.node_key = node.node_key
          AND newer.attempt > node.attempt
      )
  ), 0) AS integer) AS activity_completed
FROM workflow_instance wi
JOIN issue host
  ON host.id = wi.host_issue_id
 AND host.workspace_id = wi.workspace_id
JOIN workflow_template template
  ON template.id = wi.template_id
 AND template.workspace_id = wi.workspace_id
JOIN workflow_template_version version
  ON version.id = wi.template_version_id
 AND version.workspace_id = wi.workspace_id
WHERE wi.workspace_id = @workspace_id
  AND wi.id = ANY(@workflow_instance_ids::uuid[]);

-- name: ListWorkflowCurrentActivitiesForInstances :many
SELECT
  node.workflow_instance_id,
  node.id,
  node.node_key,
  node.name_snapshot,
  node.status,
  node.attempt
FROM workflow_node_instance node
WHERE node.workspace_id = @workspace_id
  AND node.workflow_instance_id = ANY(@workflow_instance_ids::uuid[])
  AND node.status IN ('active', 'waiting', 'blocked')
  AND NOT EXISTS (
    SELECT 1 FROM workflow_node_instance newer
    WHERE newer.workflow_instance_id = node.workflow_instance_id
      AND newer.workspace_id = node.workspace_id
      AND newer.node_key = node.node_key
      AND newer.attempt > node.attempt
  )
ORDER BY node.workflow_instance_id, node.display_order, node.node_key;

-- name: ListWorkflowCurrentOwnersForInstances :many
SELECT
  node.workflow_instance_id,
  owner.actor_type,
  owner.actor_id
FROM workflow_node_participant owner
JOIN workflow_node_instance node
  ON node.id = owner.workflow_node_instance_id
 AND node.workspace_id = owner.workspace_id
WHERE node.workspace_id = @workspace_id
  AND node.workflow_instance_id = ANY(@workflow_instance_ids::uuid[])
  AND node.status IN ('active', 'waiting', 'blocked')
  AND owner.role = 'owner'
ORDER BY node.workflow_instance_id, owner.created_at, owner.actor_id;

-- name: ListWorkflowCurrentMemberParticipantsForInstances :many
SELECT
  node.workflow_instance_id,
  node.id AS workflow_node_instance_id,
  participant.role,
  participant.actor_id
FROM workflow_node_participant participant
JOIN workflow_node_instance node
  ON node.id = participant.workflow_node_instance_id
 AND node.workspace_id = participant.workspace_id
WHERE node.workspace_id = @workspace_id
  AND node.workflow_instance_id = ANY(@workflow_instance_ids::uuid[])
  AND node.status IN ('active', 'waiting', 'blocked')
  AND participant.actor_type = 'member'
  AND participant.actor_id = @viewer_id
ORDER BY
  node.workflow_instance_id,
  node.display_order,
  participant.role,
  participant.created_at,
  participant.actor_id;

-- name: ListWorkflowNodeInstancesForInstances :many
SELECT *
FROM workflow_node_instance
WHERE workspace_id = @workspace_id
  AND workflow_instance_id = ANY(@workflow_instance_ids::uuid[])
ORDER BY workflow_instance_id, display_order, node_key, attempt;

-- name: ListWorkflowTasksForInstances :many
SELECT *
FROM workflow_node_task
WHERE workspace_id = @workspace_id
  AND workflow_instance_id = ANY(@workflow_instance_ids::uuid[])
ORDER BY workflow_instance_id, created_at, id;

-- name: ListWorkflowInstances :many
SELECT wi.*
FROM workflow_instance wi
CROSS JOIN LATERAL (
  SELECT CASE
    WHEN wi.status = 'paused' THEN 'resume'
    WHEN wi.status = 'failed' THEN 'reconcile'
    WHEN wi.status IN ('running', 'needs_setup') AND EXISTS (
      SELECT 1 FROM workflow_node_task task
      WHERE task.workflow_instance_id = wi.id
        AND task.workspace_id = wi.workspace_id
        AND task.materialization_status = 'failed'
    ) THEN 'retry_materialization'
    WHEN wi.status IN ('running', 'needs_setup') AND EXISTS (
      SELECT 1 FROM workflow_node_task task
      WHERE task.workflow_instance_id = wi.id
        AND task.workspace_id = wi.workspace_id
        AND task.materialization_status <> 'cancelled'
        AND task.executor_resolution_id IS NULL
    ) THEN 'configure_executor'
    WHEN wi.status IN ('running', 'needs_setup') AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN ('executor_needs_setup', 'executor_unresolved')
    ) THEN 'configure_executor'
    WHEN wi.status IN ('running', 'needs_setup') AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN ('required_task_not_materialized', 'stale_materialization')
    ) THEN 'retry_materialization'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN ('awaiting_acceptance', 'acceptance_not_approved')
    ) THEN 'review_acceptance'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN (
          'task_submission_required', 'valid_submission_required',
          'submission_required_field_missing', 'submission_field_type_invalid'
        )
    ) THEN 'submit_result'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN (
          'member_verdict_required', 'verdict_definition_missing', 'verdict_not_passed'
        )
    ) THEN 'record_verdict'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' = 'confirmation_required'
    ) THEN 'confirm_activity'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' = 'manual_completion_required'
    ) THEN 'complete_activity'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node
      LEFT JOIN LATERAL jsonb_array_elements(node.waiting_reasons) reason ON true
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND (
          node.status = 'blocked'
          OR reason->>'code' = 'node_timeout'
        )
    ) THEN 'recover_activity'
    WHEN wi.status = 'needs_setup' THEN 'configure_roles'
    WHEN wi.status = 'running' THEN 'view_current_activity'
    ELSE 'none'
  END AS action
) workflow_runtime
CROSS JOIN LATERAL (
  SELECT CASE
    WHEN workflow_runtime.action IN ('none', 'view_current_activity')
      THEN workflow_runtime.action
    WHEN sqlc.arg(viewer_is_admin)::boolean
      THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'configure_roles'
      AND wi.started_by_type = 'member'
      AND wi.started_by_id = sqlc.arg(viewer_id)::uuid
      THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'configure_executor'
      AND EXISTS (
        SELECT 1
        FROM workflow_node_task task
        JOIN workflow_node_participant participant
          ON participant.workflow_node_instance_id = task.workflow_node_instance_id
         AND participant.workspace_id = task.workspace_id
        WHERE task.workflow_instance_id = wi.id
          AND task.workspace_id = wi.workspace_id
          AND task.materialization_status <> 'cancelled'
          AND task.executor_resolution_id IS NULL
          AND participant.role = 'owner'
          AND participant.actor_type = 'member'
          AND participant.actor_id = sqlc.arg(viewer_id)::uuid
      )
      THEN workflow_runtime.action
    WHEN workflow_runtime.action IN (
      'submit_result', 'record_verdict', 'complete_activity'
    ) AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node
      JOIN workflow_node_participant participant
        ON participant.workflow_node_instance_id = node.id
       AND participant.workspace_id = node.workspace_id,
      jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND participant.role = 'owner'
        AND participant.actor_type = 'member'
        AND participant.actor_id = sqlc.arg(viewer_id)::uuid
        AND (
          (
            workflow_runtime.action = 'submit_result'
            AND reason->>'code' IN (
              'task_submission_required', 'valid_submission_required',
              'submission_required_field_missing', 'submission_field_type_invalid'
            )
          )
          OR (
            workflow_runtime.action = 'record_verdict'
            AND reason->>'code' IN (
              'member_verdict_required', 'verdict_definition_missing', 'verdict_not_passed'
            )
          )
          OR (
            workflow_runtime.action = 'complete_activity'
            AND reason->>'code' = 'manual_completion_required'
          )
        )
    ) THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'review_acceptance'
      AND EXISTS (
        SELECT 1
        FROM workflow_node_instance node
        JOIN workflow_node_participant participant
          ON participant.workflow_node_instance_id = node.id
         AND participant.workspace_id = node.workspace_id,
        jsonb_array_elements(node.waiting_reasons) reason
        WHERE node.workflow_instance_id = wi.id
          AND node.workspace_id = wi.workspace_id
          AND node.status IN ('active', 'waiting', 'blocked')
          AND participant.role = 'approver'
          AND participant.actor_type = 'member'
          AND participant.actor_id = sqlc.arg(viewer_id)::uuid
          AND reason->>'code' IN ('awaiting_acceptance', 'acceptance_not_approved')
      )
      THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'confirm_activity'
      AND EXISTS (
        SELECT 1
        FROM workflow_node_instance node
        WHERE node.workflow_instance_id = wi.id
          AND node.workspace_id = wi.workspace_id
          AND node.status IN ('active', 'waiting', 'blocked')
          AND EXISTS (
            SELECT 1
            FROM jsonb_array_elements(node.waiting_reasons) reason
            WHERE reason->>'code' = 'confirmation_required'
          )
          AND (
            node.definition_snapshot #>> '{completion,confirmation}' = 'member_any'
            OR (
              node.definition_snapshot #>> '{completion,confirmation}' = 'member_all'
              AND EXISTS (
                SELECT 1 FROM workflow_node_participant participant
                WHERE participant.workflow_node_instance_id = node.id
                  AND participant.workspace_id = node.workspace_id
                  AND participant.actor_type = 'member'
                  AND participant.actor_id = sqlc.arg(viewer_id)::uuid
              )
            )
            OR (
              node.definition_snapshot #>> '{completion,confirmation}' IN (
                'owner_any', 'owner_all'
              )
              AND EXISTS (
                SELECT 1 FROM workflow_node_participant participant
                WHERE participant.workflow_node_instance_id = node.id
                  AND participant.workspace_id = node.workspace_id
                  AND participant.role = 'owner'
                  AND participant.actor_type = 'member'
                  AND participant.actor_id = sqlc.arg(viewer_id)::uuid
              )
            )
          )
      )
      THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'recover_activity'
      AND EXISTS (
        SELECT 1
        FROM workflow_node_instance node
        JOIN workflow_node_participant participant
          ON participant.workflow_node_instance_id = node.id
         AND participant.workspace_id = node.workspace_id
        WHERE node.workflow_instance_id = wi.id
          AND node.workspace_id = wi.workspace_id
          AND node.status IN ('active', 'waiting', 'blocked')
          AND participant.role = 'owner'
          AND participant.actor_type = 'member'
          AND participant.actor_id = sqlc.arg(viewer_id)::uuid
          AND (
            node.status = 'blocked'
            OR EXISTS (
              SELECT 1 FROM jsonb_array_elements(node.waiting_reasons) reason
              WHERE reason->>'code' = 'node_timeout'
            )
          )
      )
      THEN workflow_runtime.action
    ELSE 'view_current_activity'
  END AS action
) workflow_personalized
WHERE wi.workspace_id = @workspace_id
  AND (
    sqlc.narg(status)::text IS NULL
    OR (
      sqlc.narg(status) = 'active'
      AND wi.status IN ('needs_setup', 'running', 'paused', 'failed')
    )
    OR (
      sqlc.narg(status) = 'terminal'
      AND wi.status IN ('completed', 'cancelled')
    )
    OR wi.status = sqlc.narg(status)
  )
  AND (sqlc.narg(project_id)::uuid IS NULL OR EXISTS (
    SELECT 1 FROM issue host
    WHERE host.id = wi.host_issue_id
      AND host.workspace_id = wi.workspace_id
      AND host.project_id = sqlc.narg(project_id)
  ))
  AND (sqlc.narg(template_id)::uuid IS NULL OR wi.template_id = sqlc.narg(template_id))
  AND (sqlc.narg(current_node_key)::text IS NULL OR EXISTS (
    SELECT 1 FROM workflow_node_instance current_node
    WHERE current_node.workflow_instance_id = wi.id
      AND current_node.workspace_id = wi.workspace_id
      AND current_node.node_key = sqlc.narg(current_node_key)
      AND current_node.status IN ('active', 'waiting', 'blocked')
  ))
  AND (
    sqlc.narg(owner_id)::uuid IS NULL
    OR EXISTS (
      SELECT 1 FROM workflow_node_participant owner_filter
      JOIN workflow_node_instance owner_node
        ON owner_node.id = owner_filter.workflow_node_instance_id
       AND owner_node.workspace_id = owner_filter.workspace_id
      WHERE owner_node.workflow_instance_id = wi.id
        AND owner_node.status IN ('active', 'waiting', 'blocked')
        AND owner_filter.role = 'owner'
        AND owner_filter.actor_type = sqlc.narg(owner_type)
        AND owner_filter.actor_id = sqlc.narg(owner_id)
    )
  )
  AND (
    NOT sqlc.arg(related_to_me)::boolean
    OR EXISTS (
      SELECT 1 FROM workflow_instance_role_assignment role_assignment
      WHERE role_assignment.workflow_instance_id = wi.id
        AND role_assignment.workspace_id = wi.workspace_id
        AND role_assignment.actor_type = 'member'
        AND role_assignment.actor_id = sqlc.arg(viewer_id)::uuid
    )
    OR wi.started_by_id = sqlc.arg(viewer_id)::uuid
    OR EXISTS (
      SELECT 1 FROM workflow_node_participant participant
      JOIN workflow_node_instance participant_node
        ON participant_node.id = participant.workflow_node_instance_id
       AND participant_node.workspace_id = participant.workspace_id
      WHERE participant_node.workflow_instance_id = wi.id
        AND participant.workspace_id = wi.workspace_id
        AND participant.actor_type = 'member'
        AND participant.actor_id = sqlc.arg(viewer_id)::uuid
    )
    OR EXISTS (
      SELECT 1 FROM workflow_node_task assigned_task
      JOIN issue assigned_issue
        ON assigned_issue.id = assigned_task.issue_id
       AND assigned_issue.workspace_id = assigned_task.workspace_id
      WHERE assigned_task.workflow_instance_id = wi.id
        AND assigned_task.workspace_id = wi.workspace_id
        AND assigned_issue.assignee_type = 'member'
        AND assigned_issue.assignee_id = sqlc.arg(viewer_id)::uuid
    )
  )
  AND (
    sqlc.narg(intervention_type)::text IS NULL
    OR workflow_personalized.action = sqlc.narg(intervention_type)
  )
  AND (
    sqlc.narg(cursor_updated_at)::timestamptz IS NULL
    OR CASE
      WHEN workflow_personalized.action IN ('none', 'view_current_activity') THEN 1
      ELSE 0
    END > sqlc.arg(cursor_intervention_rank)::integer
    OR (
      CASE
        WHEN workflow_personalized.action IN ('none', 'view_current_activity') THEN 1
        ELSE 0
      END = sqlc.arg(cursor_intervention_rank)::integer
      AND (wi.updated_at, wi.id) < (
        sqlc.narg(cursor_updated_at)::timestamptz,
        sqlc.narg(cursor_id)::uuid
      )
    )
  )
ORDER BY
  CASE
    WHEN workflow_personalized.action IN ('none', 'view_current_activity') THEN 1
    ELSE 0
  END,
  wi.updated_at DESC,
  wi.id DESC
LIMIT @row_limit;

-- name: CountWorkflowInstances :one
SELECT count(*)::bigint
FROM workflow_instance wi
CROSS JOIN LATERAL (
  SELECT CASE
    WHEN wi.status = 'paused' THEN 'resume'
    WHEN wi.status = 'failed' THEN 'reconcile'
    WHEN wi.status IN ('running', 'needs_setup') AND EXISTS (
      SELECT 1 FROM workflow_node_task task
      WHERE task.workflow_instance_id = wi.id
        AND task.workspace_id = wi.workspace_id
        AND task.materialization_status = 'failed'
    ) THEN 'retry_materialization'
    WHEN wi.status IN ('running', 'needs_setup') AND EXISTS (
      SELECT 1 FROM workflow_node_task task
      WHERE task.workflow_instance_id = wi.id
        AND task.workspace_id = wi.workspace_id
        AND task.materialization_status <> 'cancelled'
        AND task.executor_resolution_id IS NULL
    ) THEN 'configure_executor'
    WHEN wi.status IN ('running', 'needs_setup') AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN ('executor_needs_setup', 'executor_unresolved')
    ) THEN 'configure_executor'
    WHEN wi.status IN ('running', 'needs_setup') AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN ('required_task_not_materialized', 'stale_materialization')
    ) THEN 'retry_materialization'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN ('awaiting_acceptance', 'acceptance_not_approved')
    ) THEN 'review_acceptance'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN (
          'task_submission_required', 'valid_submission_required',
          'submission_required_field_missing', 'submission_field_type_invalid'
        )
    ) THEN 'submit_result'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' IN (
          'member_verdict_required', 'verdict_definition_missing', 'verdict_not_passed'
        )
    ) THEN 'record_verdict'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' = 'confirmation_required'
    ) THEN 'confirm_activity'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node,
           jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND reason->>'code' = 'manual_completion_required'
    ) THEN 'complete_activity'
    WHEN wi.status = 'running' AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node
      LEFT JOIN LATERAL jsonb_array_elements(node.waiting_reasons) reason ON true
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND (
          node.status = 'blocked'
          OR reason->>'code' = 'node_timeout'
        )
    ) THEN 'recover_activity'
    WHEN wi.status = 'needs_setup' THEN 'configure_roles'
    WHEN wi.status = 'running' THEN 'view_current_activity'
    ELSE 'none'
  END AS action
) workflow_runtime
CROSS JOIN LATERAL (
  SELECT CASE
    WHEN workflow_runtime.action IN ('none', 'view_current_activity')
      THEN workflow_runtime.action
    WHEN sqlc.arg(viewer_is_admin)::boolean
      THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'configure_roles'
      AND wi.started_by_type = 'member'
      AND wi.started_by_id = sqlc.arg(viewer_id)::uuid
      THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'configure_executor'
      AND EXISTS (
        SELECT 1
        FROM workflow_node_task task
        JOIN workflow_node_participant participant
          ON participant.workflow_node_instance_id = task.workflow_node_instance_id
         AND participant.workspace_id = task.workspace_id
        WHERE task.workflow_instance_id = wi.id
          AND task.workspace_id = wi.workspace_id
          AND task.materialization_status <> 'cancelled'
          AND task.executor_resolution_id IS NULL
          AND participant.role = 'owner'
          AND participant.actor_type = 'member'
          AND participant.actor_id = sqlc.arg(viewer_id)::uuid
      )
      THEN workflow_runtime.action
    WHEN workflow_runtime.action IN (
      'submit_result', 'record_verdict', 'complete_activity'
    ) AND EXISTS (
      SELECT 1
      FROM workflow_node_instance node
      JOIN workflow_node_participant participant
        ON participant.workflow_node_instance_id = node.id
       AND participant.workspace_id = node.workspace_id,
      jsonb_array_elements(node.waiting_reasons) reason
      WHERE node.workflow_instance_id = wi.id
        AND node.workspace_id = wi.workspace_id
        AND node.status IN ('active', 'waiting', 'blocked')
        AND participant.role = 'owner'
        AND participant.actor_type = 'member'
        AND participant.actor_id = sqlc.arg(viewer_id)::uuid
        AND (
          (
            workflow_runtime.action = 'submit_result'
            AND reason->>'code' IN (
              'task_submission_required', 'valid_submission_required',
              'submission_required_field_missing', 'submission_field_type_invalid'
            )
          )
          OR (
            workflow_runtime.action = 'record_verdict'
            AND reason->>'code' IN (
              'member_verdict_required', 'verdict_definition_missing', 'verdict_not_passed'
            )
          )
          OR (
            workflow_runtime.action = 'complete_activity'
            AND reason->>'code' = 'manual_completion_required'
          )
        )
    ) THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'review_acceptance'
      AND EXISTS (
        SELECT 1
        FROM workflow_node_instance node
        JOIN workflow_node_participant participant
          ON participant.workflow_node_instance_id = node.id
         AND participant.workspace_id = node.workspace_id,
        jsonb_array_elements(node.waiting_reasons) reason
        WHERE node.workflow_instance_id = wi.id
          AND node.workspace_id = wi.workspace_id
          AND node.status IN ('active', 'waiting', 'blocked')
          AND participant.role = 'approver'
          AND participant.actor_type = 'member'
          AND participant.actor_id = sqlc.arg(viewer_id)::uuid
          AND reason->>'code' IN ('awaiting_acceptance', 'acceptance_not_approved')
      )
      THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'confirm_activity'
      AND EXISTS (
        SELECT 1
        FROM workflow_node_instance node
        WHERE node.workflow_instance_id = wi.id
          AND node.workspace_id = wi.workspace_id
          AND node.status IN ('active', 'waiting', 'blocked')
          AND EXISTS (
            SELECT 1
            FROM jsonb_array_elements(node.waiting_reasons) reason
            WHERE reason->>'code' = 'confirmation_required'
          )
          AND (
            node.definition_snapshot #>> '{completion,confirmation}' = 'member_any'
            OR (
              node.definition_snapshot #>> '{completion,confirmation}' = 'member_all'
              AND EXISTS (
                SELECT 1 FROM workflow_node_participant participant
                WHERE participant.workflow_node_instance_id = node.id
                  AND participant.workspace_id = node.workspace_id
                  AND participant.actor_type = 'member'
                  AND participant.actor_id = sqlc.arg(viewer_id)::uuid
              )
            )
            OR (
              node.definition_snapshot #>> '{completion,confirmation}' IN (
                'owner_any', 'owner_all'
              )
              AND EXISTS (
                SELECT 1 FROM workflow_node_participant participant
                WHERE participant.workflow_node_instance_id = node.id
                  AND participant.workspace_id = node.workspace_id
                  AND participant.role = 'owner'
                  AND participant.actor_type = 'member'
                  AND participant.actor_id = sqlc.arg(viewer_id)::uuid
              )
            )
          )
      )
      THEN workflow_runtime.action
    WHEN workflow_runtime.action = 'recover_activity'
      AND EXISTS (
        SELECT 1
        FROM workflow_node_instance node
        JOIN workflow_node_participant participant
          ON participant.workflow_node_instance_id = node.id
         AND participant.workspace_id = node.workspace_id
        WHERE node.workflow_instance_id = wi.id
          AND node.workspace_id = wi.workspace_id
          AND node.status IN ('active', 'waiting', 'blocked')
          AND participant.role = 'owner'
          AND participant.actor_type = 'member'
          AND participant.actor_id = sqlc.arg(viewer_id)::uuid
          AND (
            node.status = 'blocked'
            OR EXISTS (
              SELECT 1 FROM jsonb_array_elements(node.waiting_reasons) reason
              WHERE reason->>'code' = 'node_timeout'
            )
          )
      )
      THEN workflow_runtime.action
    ELSE 'view_current_activity'
  END AS action
) workflow_personalized
WHERE wi.workspace_id = @workspace_id
  AND (
    sqlc.narg(status)::text IS NULL
    OR (
      sqlc.narg(status) = 'active'
      AND wi.status IN ('needs_setup', 'running', 'paused', 'failed')
    )
    OR (
      sqlc.narg(status) = 'terminal'
      AND wi.status IN ('completed', 'cancelled')
    )
    OR wi.status = sqlc.narg(status)
  )
  AND (sqlc.narg(project_id)::uuid IS NULL OR EXISTS (
    SELECT 1 FROM issue host
    WHERE host.id = wi.host_issue_id
      AND host.workspace_id = wi.workspace_id
      AND host.project_id = sqlc.narg(project_id)
  ))
  AND (sqlc.narg(template_id)::uuid IS NULL OR wi.template_id = sqlc.narg(template_id))
  AND (sqlc.narg(current_node_key)::text IS NULL OR EXISTS (
    SELECT 1 FROM workflow_node_instance current_node
    WHERE current_node.workflow_instance_id = wi.id
      AND current_node.workspace_id = wi.workspace_id
      AND current_node.node_key = sqlc.narg(current_node_key)
      AND current_node.status IN ('active', 'waiting', 'blocked')
  ))
  AND (
    sqlc.narg(owner_id)::uuid IS NULL
    OR EXISTS (
      SELECT 1 FROM workflow_node_participant owner_filter
      JOIN workflow_node_instance owner_node
        ON owner_node.id = owner_filter.workflow_node_instance_id
       AND owner_node.workspace_id = owner_filter.workspace_id
      WHERE owner_node.workflow_instance_id = wi.id
        AND owner_node.status IN ('active', 'waiting', 'blocked')
        AND owner_filter.role = 'owner'
        AND owner_filter.actor_type = sqlc.narg(owner_type)
        AND owner_filter.actor_id = sqlc.narg(owner_id)
    )
  )
  AND (
    NOT sqlc.arg(related_to_me)::boolean
    OR EXISTS (
      SELECT 1 FROM workflow_instance_role_assignment role_assignment
      WHERE role_assignment.workflow_instance_id = wi.id
        AND role_assignment.workspace_id = wi.workspace_id
        AND role_assignment.actor_type = 'member'
        AND role_assignment.actor_id = sqlc.arg(viewer_id)::uuid
    )
    OR wi.started_by_id = sqlc.arg(viewer_id)::uuid
    OR EXISTS (
      SELECT 1 FROM workflow_node_participant participant
      JOIN workflow_node_instance participant_node
        ON participant_node.id = participant.workflow_node_instance_id
       AND participant_node.workspace_id = participant.workspace_id
      WHERE participant_node.workflow_instance_id = wi.id
        AND participant.workspace_id = wi.workspace_id
        AND participant.actor_type = 'member'
        AND participant.actor_id = sqlc.arg(viewer_id)::uuid
    )
    OR EXISTS (
      SELECT 1 FROM workflow_node_task assigned_task
      JOIN issue assigned_issue
        ON assigned_issue.id = assigned_task.issue_id
       AND assigned_issue.workspace_id = assigned_task.workspace_id
      WHERE assigned_task.workflow_instance_id = wi.id
        AND assigned_task.workspace_id = wi.workspace_id
        AND assigned_issue.assignee_type = 'member'
        AND assigned_issue.assignee_id = sqlc.arg(viewer_id)::uuid
    )
  )
  AND (
    sqlc.narg(intervention_type)::text IS NULL
    OR workflow_personalized.action = sqlc.narg(intervention_type)
  );

-- name: GetWorkflowInstanceInWorkspace :one
SELECT * FROM workflow_instance
WHERE id = @id AND workspace_id = @workspace_id;

-- name: GetActiveWorkflowInstanceByHost :one
SELECT * FROM workflow_instance
WHERE host_issue_id = @host_issue_id
  AND workspace_id = @workspace_id
  AND status IN ('needs_setup', 'running', 'paused')
LIMIT 1;

-- name: GetLatestWorkflowInstanceByHost :one
SELECT * FROM workflow_instance
WHERE host_issue_id = @host_issue_id
  AND workspace_id = @workspace_id
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: LockWorkflowInstance :one
SELECT * FROM workflow_instance
WHERE id = @id AND workspace_id = @workspace_id
FOR UPDATE;

-- name: CreateWorkflowInstance :one
INSERT INTO workflow_instance (
    workspace_id, template_id, template_version_id, host_issue_id,
    status, host_status_mode, input, started_by_type, started_by_id
) VALUES (
    @workspace_id, @template_id, @template_version_id, @host_issue_id,
    @status, @host_status_mode, @input, @started_by_type, sqlc.narg(started_by_id)
)
RETURNING *;

-- name: UpdateWorkflowInstanceState :one
UPDATE workflow_instance
SET status = @status,
    result = COALESCE(sqlc.narg(result), result),
    revision = revision + 1,
    paused_at = CASE WHEN @status::text = 'paused' THEN now() ELSE paused_at END,
    completed_at = CASE WHEN @status::text = 'completed' THEN now() ELSE completed_at END,
    cancelled_at = CASE WHEN @status::text = 'cancelled' THEN now() ELSE cancelled_at END,
    last_reconciled_at = CASE WHEN sqlc.arg(mark_reconciled)::boolean THEN now() ELSE last_reconciled_at END,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND revision = @expected_revision
RETURNING *;

-- name: CreateWorkflowRoleAssignment :one
INSERT INTO workflow_instance_role_assignment (
    workspace_id, workflow_instance_id, role_key, actor_type, actor_id, source
) VALUES (
    @workspace_id, @workflow_instance_id, @role_key, @actor_type, @actor_id, @source
)
RETURNING *;

-- name: ListWorkflowRoleAssignments :many
SELECT * FROM workflow_instance_role_assignment
WHERE workflow_instance_id = @workflow_instance_id AND workspace_id = @workspace_id
ORDER BY role_key;

-- name: UpsertWorkflowRoleAssignment :one
INSERT INTO workflow_instance_role_assignment (
    workspace_id, workflow_instance_id, role_key, actor_type, actor_id, source
) VALUES (
    @workspace_id, @workflow_instance_id, @role_key, @actor_type, @actor_id, @source
)
ON CONFLICT (workflow_instance_id, role_key)
DO UPDATE SET actor_type = EXCLUDED.actor_type,
              actor_id = EXCLUDED.actor_id,
              source = EXCLUDED.source,
              updated_at = now()
RETURNING *;

-- name: CreateWorkflowNodeInstance :one
INSERT INTO workflow_node_instance (
    workspace_id, workflow_instance_id, node_key, node_kind, attempt,
    name_snapshot, display_order, definition_snapshot, status
) VALUES (
    @workspace_id, @workflow_instance_id, @node_key, @node_kind, @attempt,
    @name_snapshot, @display_order, @definition_snapshot, @status
)
RETURNING *;

-- name: ListWorkflowNodeInstances :many
SELECT * FROM workflow_node_instance
WHERE workflow_instance_id = @workflow_instance_id AND workspace_id = @workspace_id
ORDER BY display_order, attempt;

-- name: GetWorkflowNodeInstanceInWorkspace :one
SELECT * FROM workflow_node_instance
WHERE id = @id AND workspace_id = @workspace_id;

-- name: GetLatestWorkflowNodeAttempt :one
SELECT * FROM workflow_node_instance
WHERE workflow_instance_id = @workflow_instance_id
  AND workspace_id = @workspace_id
  AND node_key = @node_key
ORDER BY attempt DESC
LIMIT 1;

-- name: UpdateWorkflowNodeState :one
UPDATE workflow_node_instance
SET status = @status,
    waiting_reasons = COALESCE(sqlc.narg(waiting_reasons), waiting_reasons),
    activated_at = CASE WHEN @status::text IN ('active', 'waiting') AND activated_at IS NULL THEN now() ELSE activated_at END,
    completed_at = CASE WHEN @status::text = 'completed' THEN now() ELSE completed_at END,
    superseded_at = CASE WHEN @status::text = 'superseded' THEN now() ELSE superseded_at END,
    last_reconciled_at = CASE WHEN sqlc.arg(mark_reconciled)::boolean THEN now() ELSE last_reconciled_at END,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = @expected_status
RETURNING *;

-- name: CancelOpenWorkflowNodes :exec
UPDATE workflow_node_instance
SET status = 'cancelled', updated_at = now()
WHERE workflow_instance_id = @workflow_instance_id
  AND workspace_id = @workspace_id
  AND status IN ('pending', 'ready', 'active', 'waiting', 'blocked');

-- name: CreateWorkflowNodeParticipant :one
INSERT INTO workflow_node_participant (
    workspace_id, workflow_node_instance_id, role, actor_type, actor_id
) VALUES (
    @workspace_id, @workflow_node_instance_id, @role, @actor_type, @actor_id
)
RETURNING *;

-- name: ListWorkflowNodeParticipants :many
SELECT * FROM workflow_node_participant
WHERE workflow_node_instance_id = @workflow_node_instance_id
  AND workspace_id = @workspace_id
ORDER BY role, created_at, id;

-- name: CreateWorkflowExecutorResolution :one
INSERT INTO workflow_executor_resolution (
    workspace_id, workflow_instance_id, workflow_node_instance_id,
    workflow_node_task_id, strategy, status, actor_type, actor_id,
    candidates, reason, definition_snapshot, resolved_at
) VALUES (
    @workspace_id, @workflow_instance_id, @workflow_node_instance_id,
    sqlc.narg(workflow_node_task_id), @strategy, @status,
    sqlc.narg(actor_type), sqlc.narg(actor_id), @candidates, @reason,
    @definition_snapshot, sqlc.narg(resolved_at)
)
RETURNING *;

-- name: ListWorkflowExecutorResolutions :many
SELECT * FROM workflow_executor_resolution
WHERE workflow_node_instance_id = @workflow_node_instance_id
  AND workspace_id = @workspace_id
ORDER BY created_at, id;

-- name: GetWorkflowExecutorResolutionInWorkspace :one
SELECT * FROM workflow_executor_resolution
WHERE id = @id AND workspace_id = @workspace_id;

-- name: CreateWorkflowNodeTask :one
INSERT INTO workflow_node_task (
    workspace_id, workflow_instance_id, workflow_node_instance_id,
    task_key, source, required, definition_snapshot, materialization_status,
    created_by_type, created_by_id
) VALUES (
    @workspace_id, @workflow_instance_id, @workflow_node_instance_id,
    @task_key, @source, @required, @definition_snapshot, @materialization_status,
    @created_by_type, sqlc.narg(created_by_id)
)
RETURNING *;

-- name: ListWorkflowNodeTasks :many
SELECT * FROM workflow_node_task
WHERE workflow_node_instance_id = @workflow_node_instance_id AND workspace_id = @workspace_id
ORDER BY created_at, id;

-- name: ListWorkflowInstanceTasks :many
SELECT * FROM workflow_node_task
WHERE workflow_instance_id = @workflow_instance_id AND workspace_id = @workspace_id
ORDER BY created_at, id;

-- name: GetWorkflowNodeTaskInWorkspace :one
SELECT * FROM workflow_node_task
WHERE id = @id AND workspace_id = @workspace_id;

-- name: GetWorkflowNodeTaskByIssue :one
SELECT * FROM workflow_node_task
WHERE issue_id = @issue_id AND workspace_id = @workspace_id
LIMIT 1;

-- name: GetActiveWorkflowIssueBinding :one
SELECT
    task.id AS workflow_node_task_id,
    task.required,
    node.id AS workflow_node_instance_id,
    node.status AS node_status,
    instance.id AS workflow_instance_id,
    instance.status AS instance_status,
    instance.host_issue_id
FROM workflow_node_task task
JOIN workflow_node_instance node
  ON node.id = task.workflow_node_instance_id
 AND node.workspace_id = task.workspace_id
JOIN workflow_instance instance
  ON instance.id = task.workflow_instance_id
 AND instance.workspace_id = task.workspace_id
WHERE task.issue_id = @issue_id
  AND task.workspace_id = @workspace_id
  AND instance.status IN ('needs_setup', 'running', 'paused')
  AND node.status IN ('ready', 'active', 'waiting', 'blocked')
LIMIT 1;

-- name: ListWorkflowInstanceIssues :many
SELECT i.*
FROM workflow_node_task t
JOIN issue i ON i.id = t.issue_id AND i.workspace_id = t.workspace_id
WHERE t.workflow_instance_id = @workflow_instance_id
  AND t.workspace_id = @workspace_id
ORDER BY t.created_at, t.id;

-- name: ListWorkflowNodeIssues :many
SELECT i.*
FROM workflow_node_task t
JOIN issue i ON i.id = t.issue_id AND i.workspace_id = t.workspace_id
WHERE t.workflow_node_instance_id = @workflow_node_instance_id
  AND t.workspace_id = @workspace_id
ORDER BY t.created_at, t.id;

-- name: BindWorkflowNodeTaskIssue :one
UPDATE workflow_node_task
SET issue_id = @issue_id,
    materialization_status = 'materialized',
    claimed_at = NULL,
    last_error = '',
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: SetWorkflowNodeTaskExecutorResolution :one
UPDATE workflow_node_task
SET executor_resolution_id = @executor_resolution_id,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkWorkflowNodeTaskMaterializing :one
UPDATE workflow_node_task
SET materialization_status = 'materializing',
    attempt_count = attempt_count + 1,
    claimed_at = now(),
    last_error = '',
    updated_at = now()
WHERE id = @id
  AND workspace_id = @workspace_id
  AND materialization_status IN ('pending_materialization', 'failed')
RETURNING *;

-- name: MarkWorkflowNodeTaskMaterializationFailed :one
UPDATE workflow_node_task
SET materialization_status = 'failed',
    claimed_at = NULL,
    last_error = @last_error,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: ResetWorkflowNodeTaskMaterialization :one
UPDATE workflow_node_task
SET materialization_status = 'pending_materialization',
    claimed_at = NULL,
    last_error = '',
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: ReleaseWorkflowNodeTaskMaterializationClaim :one
UPDATE workflow_node_task
SET materialization_status = 'pending_materialization',
    attempt_count = GREATEST(attempt_count - 1, 0),
    claimed_at = NULL,
    last_error = '',
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
  AND materialization_status = 'materializing'
RETURNING *;

-- name: ClaimWorkflowNodeTaskForMaterialization :one
WITH candidate AS (
    SELECT task.id
    FROM workflow_node_task task
    JOIN workflow_instance instance
      ON instance.id = task.workflow_instance_id
     AND instance.workspace_id = task.workspace_id
    JOIN workflow_node_instance node
      ON node.id = task.workflow_node_instance_id
     AND node.workspace_id = task.workspace_id
    JOIN workflow_executor_resolution resolution
      ON resolution.id = task.executor_resolution_id
     AND resolution.workspace_id = task.workspace_id
     AND resolution.workflow_node_task_id = task.id
     AND resolution.status = 'resolved'
    WHERE instance.status = 'running'
      AND node.status IN ('active', 'waiting', 'blocked')
      AND task.issue_id IS NULL
      AND task.attempt_count < @max_attempts
      AND (
        task.materialization_status = 'pending_materialization'
        OR (
          task.materialization_status = 'failed'
          AND task.updated_at <= now() - make_interval(
            secs => LEAST(60, power(2, LEAST(task.attempt_count, 6))::integer)
          )
        )
      )
    ORDER BY task.updated_at, task.id
    FOR UPDATE OF task SKIP LOCKED
    LIMIT 1
)
UPDATE workflow_node_task task
SET materialization_status = 'materializing',
    attempt_count = task.attempt_count + 1,
    claimed_at = now(),
    last_error = '',
    updated_at = now()
FROM candidate
WHERE task.id = candidate.id
RETURNING task.*;

-- name: ClaimWorkflowInstanceForReconcile :one
WITH candidate AS (
    SELECT instance.id
    FROM workflow_instance instance
    WHERE (
        instance.reconcile_after IS NULL
        OR instance.reconcile_after <= now()
      )
      AND (
        (
          instance.status = 'running'
          AND (
            instance.last_reconciled_at IS NULL
            OR instance.last_reconciled_at < now() - make_interval(secs => @minimum_interval_seconds)
          )
          AND (
            (
              instance.host_status_mode = 'managed'
              AND EXISTS (
                SELECT 1
                FROM issue host
                WHERE host.id = instance.host_issue_id
                  AND host.workspace_id = instance.workspace_id
                  AND host.status <> 'in_progress'
              )
            )
            OR EXISTS (
              SELECT 1
              FROM workflow_node_instance node
              LEFT JOIN workflow_node_task task
                ON task.workflow_node_instance_id = node.id
               AND task.workspace_id = node.workspace_id
              LEFT JOIN issue bound_issue
                ON bound_issue.id = task.issue_id
               AND bound_issue.workspace_id = task.workspace_id
              WHERE node.workflow_instance_id = instance.id
                AND node.workspace_id = instance.workspace_id
                AND node.status IN ('active', 'waiting', 'blocked')
                AND (
                  instance.last_reconciled_at IS NULL
                  OR node.updated_at > instance.last_reconciled_at
                  OR task.updated_at > instance.last_reconciled_at
                  OR bound_issue.updated_at > instance.last_reconciled_at
                )
            )
          )
        )
        OR (
          instance.status = 'completed'
          AND instance.host_status_mode = 'managed'
          AND (
            instance.last_reconciled_at IS NULL
            OR instance.last_reconciled_at < now() - make_interval(secs => @minimum_interval_seconds)
          )
          AND EXISTS (
            SELECT 1
            FROM issue host
            WHERE host.id = instance.host_issue_id
              AND host.workspace_id = instance.workspace_id
              AND host.status <> 'done'
          )
        )
      )
    ORDER BY instance.last_reconciled_at NULLS FIRST, instance.updated_at, instance.id
    FOR UPDATE OF instance SKIP LOCKED
    LIMIT 1
)
UPDATE workflow_instance instance
SET last_reconciled_at = now(),
    reconcile_after = NULL
FROM candidate
WHERE instance.id = candidate.id
RETURNING instance.*;

-- name: MarkWorkflowInstanceReconcilePending :one
UPDATE workflow_instance
SET last_reconciled_at = NULL,
    reconcile_after = NULL
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: DeferWorkflowInstanceReconcile :one
UPDATE workflow_instance
SET last_reconciled_at = NULL,
    reconcile_after = now() + make_interval(secs => @defer_seconds)
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: ListWorkflowNodesForSweep :many
SELECT node.*
FROM workflow_node_instance node
JOIN workflow_instance instance
  ON instance.id = node.workflow_instance_id
 AND instance.workspace_id = node.workspace_id
WHERE instance.status IN ('needs_setup', 'running')
  AND node.status IN ('active', 'waiting', 'blocked')
ORDER BY node.updated_at, node.id
LIMIT @row_limit;

-- name: ListStaleWorkflowMaterializations :many
SELECT task.*
FROM workflow_node_task task
JOIN workflow_instance instance
  ON instance.id = task.workflow_instance_id
 AND instance.workspace_id = task.workspace_id
WHERE instance.status = 'running'
  AND task.materialization_status = 'materializing'
  AND task.claimed_at < now() - make_interval(secs => @stale_after_seconds)
ORDER BY task.claimed_at, task.id
LIMIT @row_limit;

-- name: SetWorkflowNodeWaitingReasons :one
UPDATE workflow_node_instance
SET waiting_reasons = @waiting_reasons,
    updated_at = now()
WHERE id = @id
  AND workspace_id = @workspace_id
  AND status IN ('active', 'waiting', 'blocked')
RETURNING *;

-- name: DetachWorkflowNodeTask :one
UPDATE workflow_node_task
SET issue_id = NULL,
    required = false,
    materialization_status = 'cancelled',
    claimed_at = NULL,
    last_error = '',
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: DetachWorkflowNodeTasksByIssue :exec
UPDATE workflow_node_task
SET issue_id = NULL,
    required = false,
    materialization_status = 'cancelled',
    claimed_at = NULL,
    last_error = '',
    updated_at = now()
WHERE issue_id = @issue_id
  AND workspace_id = @workspace_id;

-- name: ClearWorkflowIssueOrigin :exec
UPDATE issue
SET origin_type = NULL,
    origin_id = NULL,
    updated_at = now()
WHERE id = @issue_id
  AND workspace_id = @workspace_id
  AND origin_type = 'workflow'
  AND origin_id = @workflow_node_task_id;

-- name: CreateWorkflowSubmission :one
INSERT INTO workflow_node_submission (
    workspace_id, workflow_instance_id, workflow_node_instance_id, revision,
    status, payload, summary, evidence, submitted_by_type, submitted_by_id,
    source_issue_id, source_agent_run_id, schema_version
) VALUES (
    @workspace_id, @workflow_instance_id, @workflow_node_instance_id, @revision,
    @status, @payload, @summary, @evidence, @submitted_by_type, sqlc.narg(submitted_by_id),
    sqlc.narg(source_issue_id), sqlc.narg(source_agent_run_id), @schema_version
)
RETURNING *;

-- name: ListWorkflowSubmissions :many
SELECT * FROM workflow_node_submission
WHERE workflow_node_instance_id = @workflow_node_instance_id AND workspace_id = @workspace_id
ORDER BY revision DESC;

-- name: GetWorkflowSubmissionInWorkspace :one
SELECT * FROM workflow_node_submission
WHERE id = @id AND workspace_id = @workspace_id;

-- name: GetNextWorkflowSubmissionRevision :one
SELECT COALESCE(max(revision), 0)::integer + 1
FROM workflow_node_submission
WHERE workflow_node_instance_id = @workflow_node_instance_id
  AND workspace_id = @workspace_id;

-- name: SetWorkflowNodeLatestSubmission :exec
UPDATE workflow_node_instance
SET latest_submission_id = @latest_submission_id, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id;

-- name: CreateWorkflowVerdict :one
INSERT INTO workflow_node_verdict (
    workspace_id, workflow_instance_id, workflow_node_instance_id, revision,
    result, reason, confidence, evidence, basis, evaluator_type,
    evaluator_id, definition_snapshot
) VALUES (
    @workspace_id, @workflow_instance_id, @workflow_node_instance_id, @revision,
    @result, @reason, sqlc.narg(confidence), @evidence, @basis, @evaluator_type,
    sqlc.narg(evaluator_id), @definition_snapshot
)
RETURNING *;

-- name: ListWorkflowVerdicts :many
SELECT * FROM workflow_node_verdict
WHERE workflow_node_instance_id = @workflow_node_instance_id AND workspace_id = @workspace_id
ORDER BY revision DESC;

-- name: GetWorkflowVerdictInWorkspace :one
SELECT * FROM workflow_node_verdict
WHERE id = @id AND workspace_id = @workspace_id;

-- name: GetNextWorkflowVerdictRevision :one
SELECT COALESCE(max(revision), 0)::integer + 1
FROM workflow_node_verdict
WHERE workflow_node_instance_id = @workflow_node_instance_id
  AND workspace_id = @workspace_id;

-- name: SetWorkflowNodeLatestVerdict :exec
UPDATE workflow_node_instance
SET latest_verdict_id = @latest_verdict_id, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id;

-- name: ListWorkflowNodeConfirmations :many
SELECT * FROM workflow_node_confirmation
WHERE workflow_node_instance_id = @workflow_node_instance_id
  AND workspace_id = @workspace_id
ORDER BY decided_at, id;

-- name: GetWorkflowNodeConfirmationForMember :one
SELECT * FROM workflow_node_confirmation
WHERE workflow_node_instance_id = @workflow_node_instance_id
  AND workspace_id = @workspace_id
  AND member_id = @member_id;

-- name: UpsertWorkflowNodeConfirmation :one
INSERT INTO workflow_node_confirmation (
    workspace_id, workflow_node_instance_id, member_id,
    decision, comment, decided_at
) VALUES (
    @workspace_id, @workflow_node_instance_id, @member_id,
    @decision, @comment, now()
)
ON CONFLICT (workflow_node_instance_id, member_id)
DO UPDATE SET
    decision = EXCLUDED.decision,
    comment = EXCLUDED.comment,
    decided_at = EXCLUDED.decided_at,
    updated_at = now()
RETURNING *;

-- name: CreateWorkflowAcceptance :one
INSERT INTO workflow_acceptance (
    workspace_id, workflow_instance_id, workflow_node_instance_id, revision,
    status, decided_by_type, decided_by_id, reason, rework_target_node_key,
    evidence, idempotency_key, decided_at
) VALUES (
    @workspace_id, @workflow_instance_id, @workflow_node_instance_id, @revision,
    @status, sqlc.narg(decided_by_type), sqlc.narg(decided_by_id), @reason,
    sqlc.narg(rework_target_node_key), @evidence, @idempotency_key, sqlc.narg(decided_at)
)
RETURNING *;

-- name: ListWorkflowAcceptances :many
SELECT * FROM workflow_acceptance
WHERE workflow_instance_id = @workflow_instance_id AND workspace_id = @workspace_id
ORDER BY revision DESC;

-- name: GetLatestWorkflowAcceptance :one
SELECT * FROM workflow_acceptance
WHERE workflow_instance_id = @workflow_instance_id
  AND workspace_id = @workspace_id
ORDER BY revision DESC
LIMIT 1;

-- name: GetWorkflowAcceptanceByIdempotencyKey :one
SELECT * FROM workflow_acceptance
WHERE workflow_instance_id = @workflow_instance_id
  AND workspace_id = @workspace_id
  AND idempotency_key = @idempotency_key
LIMIT 1;

-- name: GetNextWorkflowAcceptanceRevision :one
SELECT COALESCE(max(revision), 0)::integer + 1
FROM workflow_acceptance
WHERE workflow_instance_id = @workflow_instance_id
  AND workspace_id = @workspace_id;

-- name: CreateWorkflowEvent :one
INSERT INTO workflow_event (
    workspace_id, workflow_instance_id, workflow_node_instance_id,
    event_type, actor_type, actor_id, idempotency_key, payload
) VALUES (
    @workspace_id, @workflow_instance_id, sqlc.narg(workflow_node_instance_id),
    @event_type, @actor_type, sqlc.narg(actor_id), @idempotency_key, @payload
)
RETURNING *;

-- name: ListWorkflowEvents :many
SELECT * FROM workflow_event
WHERE workflow_instance_id = @workflow_instance_id AND workspace_id = @workspace_id
ORDER BY created_at DESC
LIMIT @row_limit;

-- name: GetWorkflowEventByIdempotencyKey :one
SELECT * FROM workflow_event
WHERE workflow_instance_id = @workflow_instance_id
  AND workspace_id = @workspace_id
  AND idempotency_key = @idempotency_key
LIMIT 1;

-- name: GetWorkflowNodeRoutingEvent :one
SELECT * FROM workflow_event
WHERE workflow_instance_id = @workflow_instance_id
  AND workspace_id = @workspace_id
  AND workflow_node_instance_id = @workflow_node_instance_id
  AND event_type = 'node.routed'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: GetWorkflowNodeExplicitSkipEvent :one
SELECT * FROM workflow_event
WHERE workflow_instance_id = @workflow_instance_id
  AND workspace_id = @workspace_id
  AND workflow_node_instance_id = @workflow_node_instance_id
  AND event_type = 'node.explicit_skip'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: GetWorkflowInstanceByStartIdempotencyKey :one
SELECT wi.*
FROM workflow_event we
JOIN workflow_instance wi
  ON wi.id = we.workflow_instance_id
 AND wi.workspace_id = we.workspace_id
WHERE we.workspace_id = @workspace_id
  AND we.event_type = 'workflow.started'
  AND we.idempotency_key = @idempotency_key
LIMIT 1;

-- =====================
-- Host issue deletion cleanup
-- =====================

-- name: DetachWorkflowIssuesByHost :exec
UPDATE issue child
SET origin_type = NULL,
    origin_id = NULL,
    parent_issue_id = CASE
      WHEN child.parent_issue_id = @host_issue_id THEN NULL
      ELSE child.parent_issue_id
    END,
    stage = CASE
      WHEN child.parent_issue_id = @host_issue_id THEN NULL
      ELSE child.stage
    END,
    updated_at = now()
WHERE child.workspace_id = @workspace_id
  AND child.origin_type = 'workflow'
  AND EXISTS (
    SELECT 1
    FROM workflow_node_task task
    JOIN workflow_instance instance
      ON instance.id = task.workflow_instance_id
     AND instance.workspace_id = task.workspace_id
    WHERE task.issue_id = child.id
      AND task.workspace_id = child.workspace_id
      AND instance.host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowEventsByHost :exec
DELETE FROM workflow_event event
WHERE event.workspace_id = @workspace_id
  AND event.workflow_instance_id IN (
    SELECT id FROM workflow_instance
    WHERE workspace_id = @workspace_id AND host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowAcceptancesByHost :exec
DELETE FROM workflow_acceptance acceptance
WHERE acceptance.workspace_id = @workspace_id
  AND acceptance.workflow_instance_id IN (
    SELECT id FROM workflow_instance
    WHERE workspace_id = @workspace_id AND host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowConfirmationsByHost :exec
DELETE FROM workflow_node_confirmation confirmation
WHERE confirmation.workspace_id = @workspace_id
  AND confirmation.workflow_node_instance_id IN (
    SELECT node.id
    FROM workflow_node_instance node
    JOIN workflow_instance instance
      ON instance.id = node.workflow_instance_id
     AND instance.workspace_id = node.workspace_id
    WHERE node.workspace_id = @workspace_id
      AND instance.host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowVerdictsByHost :exec
DELETE FROM workflow_node_verdict verdict
WHERE verdict.workspace_id = @workspace_id
  AND verdict.workflow_instance_id IN (
    SELECT id FROM workflow_instance
    WHERE workspace_id = @workspace_id AND host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowSubmissionsByHost :exec
DELETE FROM workflow_node_submission submission
WHERE submission.workspace_id = @workspace_id
  AND submission.workflow_instance_id IN (
    SELECT id FROM workflow_instance
    WHERE workspace_id = @workspace_id AND host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowExecutorResolutionsByHost :exec
DELETE FROM workflow_executor_resolution resolution
WHERE resolution.workspace_id = @workspace_id
  AND resolution.workflow_instance_id IN (
    SELECT id FROM workflow_instance
    WHERE workspace_id = @workspace_id AND host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowNodeTasksByHost :exec
DELETE FROM workflow_node_task task
WHERE task.workspace_id = @workspace_id
  AND task.workflow_instance_id IN (
    SELECT id FROM workflow_instance
    WHERE workspace_id = @workspace_id AND host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowNodeParticipantsByHost :exec
DELETE FROM workflow_node_participant participant
WHERE participant.workspace_id = @workspace_id
  AND participant.workflow_node_instance_id IN (
    SELECT node.id
    FROM workflow_node_instance node
    JOIN workflow_instance instance
      ON instance.id = node.workflow_instance_id
     AND instance.workspace_id = node.workspace_id
    WHERE node.workspace_id = @workspace_id
      AND instance.host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowNodesByHost :exec
DELETE FROM workflow_node_instance node
WHERE node.workspace_id = @workspace_id
  AND node.workflow_instance_id IN (
    SELECT id FROM workflow_instance
    WHERE workspace_id = @workspace_id AND host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowRoleAssignmentsByHost :exec
DELETE FROM workflow_instance_role_assignment assignment
WHERE assignment.workspace_id = @workspace_id
  AND assignment.workflow_instance_id IN (
    SELECT id FROM workflow_instance
    WHERE workspace_id = @workspace_id AND host_issue_id = @host_issue_id
  );

-- name: DeleteWorkflowInstancesByHost :exec
DELETE FROM workflow_instance
WHERE workspace_id = @workspace_id
  AND host_issue_id = @host_issue_id;
