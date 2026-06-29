-- Seed real Operations / P4 assessment demo data into the current database.
-- Idempotent by workspace slug, issue number, Feishu binding, and assessment
-- unique keys. Safe to rerun on a local/demo database.

BEGIN;

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

INSERT INTO "user" (
  name,
  email,
  onboarded_at,
  onboarding_questionnaire
)
VALUES (
  'Operations Demo',
  'operations-demo@multica.local',
  now(),
  '{"source":["friends_colleagues"],"source_other":null,"source_skipped":false,"version":2}'::jsonb
)
ON CONFLICT (email) DO UPDATE SET
  name = EXCLUDED.name,
  onboarded_at = COALESCE("user".onboarded_at, EXCLUDED.onboarded_at),
  onboarding_questionnaire = CASE
    WHEN "user".onboarding_questionnaire = '{}'::jsonb THEN EXCLUDED.onboarding_questionnaire
    ELSE "user".onboarding_questionnaire
  END,
  updated_at = now();

INSERT INTO workspace (
  name,
  slug,
  description,
  settings,
  issue_prefix,
  issue_counter
)
VALUES (
  'Operations Demo',
  'operations-demo',
  'Demo workspace for Operations P4 assessment and human review.',
  '{}'::jsonb,
  'OPS',
  0
)
ON CONFLICT (slug) DO UPDATE SET
  name = EXCLUDED.name,
  description = EXCLUDED.description,
  issue_prefix = EXCLUDED.issue_prefix,
  updated_at = now();

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
)
INSERT INTO member (workspace_id, user_id, role)
SELECT ws.id, u.id, 'owner'
FROM ws
JOIN "user" u ON true
ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = 'owner';

CREATE TEMP TABLE operations_demo_rows (
  n INTEGER PRIMARY KEY,
  work_item_id TEXT NOT NULL,
  title TEXT NOT NULL,
  project_key TEXT NOT NULL,
  workstream TEXT NOT NULL,
  version TEXT NOT NULL,
  issue_status TEXT NOT NULL,
  external_done BOOLEAN NOT NULL,
  external_status TEXT NOT NULL,
  task_status TEXT NOT NULL,
  assessment_status TEXT NOT NULL,
  attribution TEXT NOT NULL,
  quality TEXT NOT NULL,
  confidence NUMERIC(4,3),
  review_outcome TEXT NOT NULL,
  review_reasons TEXT[] NOT NULL,
  review_note TEXT NOT NULL,
  swarm_reviews JSONB NOT NULL,
  ai_shelved_cls INTEGER[] NOT NULL,
  swarm_change_cls INTEGER[] NOT NULL,
  swarm_committed_cls INTEGER[] NOT NULL,
  external_committed_cls INTEGER[] NOT NULL,
  p4_summary TEXT NOT NULL,
  p4_warnings JSONB NOT NULL,
  agent_comment TEXT NOT NULL,
  days_ago INTEGER NOT NULL
) ON COMMIT DROP;

INSERT INTO operations_demo_rows VALUES
  (
    12841,
    'BUG-93218',
    '军团科技升级后客户端显示旧等级',
    'Warpath3',
    'rel_1.7.2/server',
    '1.7.2',
    'done',
    true,
    'Done',
    'completed',
    'completed',
    'ai_delivered',
    'likely_correct',
    0.860,
    'accepted',
    ARRAY['complete_usable']::text[],
    'AI shelve 282941 已通过 Swarm 提交为最终 CL 283006，人工验收通过。',
    '[{"review_id":"SW-11872","state":"approved","changes":[282941,282944],"commits":[283006],"swarm_branch":"main","event_type":"review.committed","sent_at":"2026-06-01T00:30:00Z"}]'::jsonb,
    ARRAY[282941]::int[],
    ARRAY[282941,282944]::int[],
    ARRAY[283006]::int[],
    ARRAY[283006]::int[],
    'AI shelve was submitted as the final CL.',
    '[]'::jsonb,
    'AI 产出的 shelve 282941 已经通过 Swarm 提交为 CL 283006，飞书最终 CL 一致。',
    1
  ),
  (
    12796,
    'BUG-92877',
    'PVP 战报偶发丢失防守方增益',
    'Battle',
    'rel_1.7.2/battle',
    '1.7.2',
    'in_review',
    true,
    'Done',
    'completed',
    'completed',
    'conflict',
    'likely_needs_changes',
    0.710,
    'needs_changes',
    ARRAY['coverage_incomplete','test_insufficient']::text[],
    'AI 漏了战斗回放入口，最终 CL 282811 由人工补齐。',
    '[{"review_id":"SW-11845","state":"needsReview","changes":[282620],"commits":[282811],"swarm_branch":"release/battle","event_type":"review.committed","sent_at":"2026-06-03T03:10:00Z"}]'::jsonb,
    ARRAY[282620]::int[],
    ARRAY[282620]::int[],
    ARRAY[282811]::int[],
    ARRAY[282811]::int[],
    'AI shelve and final CL diverged; conflict prediction was useful.',
    '["final CL differs from AI shelve"]'::jsonb,
    'Multica 里有 AI shelve 282620，但飞书最终 CL 是 282811；AI 识别为冲突，人工确认 AI 漏了战斗回放入口。',
    3
  ),
  (
    12688,
    'BUG-92301',
    '雷达任务奖励重复领取',
    'Activity',
    'rel_1.7.1/activity',
    '1.7.1',
    'done',
    true,
    'Done',
    'completed',
    'completed',
    'ai_assisted',
    'likely_correct',
    0.780,
    'accepted',
    ARRAY['human_assisted']::text[],
    'AI 修复幂等检查，人工补偿脚本和线上验证后通过。',
    '[{"review_id":"SW-11791","state":"approved","changes":[281990],"commits":[282104],"swarm_branch":"release/activity","event_type":"review.committed","sent_at":"2026-06-06T08:15:00Z"}]'::jsonb,
    ARRAY[281990]::int[],
    ARRAY[281990]::int[],
    ARRAY[282104]::int[],
    ARRAY[282104]::int[],
    'AI provided the core fix; human added rollout validation.',
    '[]'::jsonb,
    'AI 修了幂等检查，人工补了补偿脚本和线上验证；最终 CL 仍基于 AI shelve。',
    6
  ),
  (
    12577,
    'BUG-91740',
    '联盟商店刷新时间跨时区错误',
    'Economy',
    'rel_1.7.1/server',
    '1.7.1',
    'in_review',
    true,
    'Done',
    'completed',
    'completed',
    'human_delivered',
    'likely_correct',
    0.640,
    'rejected',
    ARRAY['wrong_direction']::text[],
    'AI 判断最终人工 CL 正确，但人工验收发现服务端刷新根因未解决。',
    '[{"review_id":"SW-11760","state":"approved","changes":[],"commits":[281776],"swarm_branch":"release/server","event_type":"review.committed","sent_at":"2026-06-08T06:00:00Z"}]'::jsonb,
    ARRAY[]::int[],
    ARRAY[]::int[],
    ARRAY[281776]::int[],
    ARRAY[281776]::int[],
    'Final CL was human-authored and did not solve the backend root cause.',
    '["AI quality prediction overestimated the fix"]'::jsonb,
    'AI 判断最终人工 CL 看起来正确，但人工验收发现修的是 UI 显示，服务端刷新根因未解决。',
    8
  ),
  (
    12452,
    'BUG-91062',
    '新手引导任务链断在第二阶段',
    'Quest',
    'rel_1.7.0/quest',
    '1.7.0',
    'in_progress',
    true,
    'Done',
    'running',
    'running',
    'unknown',
    'unknown',
    NULL,
    'unreviewed',
    ARRAY[]::text[],
    '',
    '[{"review_id":"SW-11698","state":"needsReview","changes":[280992],"commits":[281032],"swarm_branch":"release/quest","event_type":"review.updated","sent_at":"2026-06-09T12:00:00Z"}]'::jsonb,
    ARRAY[280992]::int[],
    ARRAY[280992]::int[],
    ARRAY[281032]::int[],
    ARRAY[281032]::int[],
    'Assessment is still reading Swarm and final CL evidence.',
    '[]'::jsonb,
    'AI 正在核对 Swarm review 和飞书最终 CL 是否来自同一条链路。',
    9
  ),
  (
    12386,
    'BUG-90519',
    '地图事件刷怪数量低于配置',
    'World',
    'rel_1.6.9/world',
    '1.6.9',
    'blocked',
    true,
    'Done',
    'failed',
    'failed',
    'unknown',
    'unknown',
    NULL,
    'unreviewed',
    ARRAY[]::text[],
    '',
    '[]'::jsonb,
    ARRAY[]::int[],
    ARRAY[]::int[],
    ARRAY[]::int[],
    ARRAY[280771]::int[],
    'Assessment failed because no related Swarm review could be resolved.',
    '["missing swarm review"]'::jsonb,
    'assessment 失败：飞书字段里的 CL 能读到，但当前 issue 没有关联 Swarm review，需要人工补充线索。',
    12
  ),
  (
    12290,
    'BUG-89970',
    '军官技能升级材料扣除异常',
    'Hero',
    'rel_1.6.9/server',
    '1.6.9',
    'todo',
    true,
    'Done',
    'completed',
    'pending',
    'unknown',
    'unknown',
    NULL,
    'unreviewed',
    ARRAY[]::text[],
    '',
    '[]'::jsonb,
    ARRAY[]::int[],
    ARRAY[]::int[],
    ARRAY[]::int[],
    ARRAY[280402]::int[],
    'External item is done but AI assessment has not run yet.',
    '[]'::jsonb,
    '外部工单已完成但还没有跑 AI P4 前置判断。',
    14
  ),
  (
    12110,
    'BUG-88731',
    '赛季结算邮件重复发送',
    'Season',
    'rel_1.6.8/season',
    '1.6.8',
    'done',
    true,
    'Done',
    'completed',
    'completed',
    'unattributed',
    'unknown',
    0.390,
    'not_applicable',
    ARRAY['environment_data']::text[],
    '线上数据修复单，没有代码提交，人工标注为不适用。',
    '[{"review_id":"SW-11602","state":"archived","changes":[],"commits":[],"swarm_branch":"release/season","event_type":"review.archived","sent_at":"2026-06-11T04:00:00Z"}]'::jsonb,
    ARRAY[]::int[],
    ARRAY[]::int[],
    ARRAY[]::int[],
    ARRAY[]::int[],
    'No code CL is expected for this data repair work item.',
    '[]'::jsonb,
    '线上数据修复单，没有代码提交；人工标注为不适用。',
    18
  ),
  (
    11984,
    'BUG-87422',
    '战役章节解锁条件未及时刷新',
    'Campaign',
    'rel_1.6.7/campaign',
    '1.6.7',
    'in_review',
    false,
    'In QA',
    'completed',
    'completed',
    'ai_delivered',
    'likely_correct',
    0.910,
    'unreviewed',
    ARRAY[]::text[],
    '',
    '[{"review_id":"SW-11540","state":"approved","changes":[279880],"commits":[279914],"swarm_branch":"release/campaign","event_type":"review.committed","sent_at":"2026-06-13T09:20:00Z"}]'::jsonb,
    ARRAY[279880]::int[],
    ARRAY[279880]::int[],
    ARRAY[279914]::int[],
    ARRAY[279914]::int[],
    'AI fix looks good but external item is not done, so it stays out of stats.',
    '[]'::jsonb,
    'AI 已修完，但飞书未完成，不进入统计。',
    20
  );

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
),
demo_user AS (
  SELECT id FROM "user" WHERE email = 'operations-demo@multica.local'
),
runtime AS (
  INSERT INTO agent_runtime (
    workspace_id,
    daemon_id,
    name,
    runtime_mode,
    provider,
    status,
    device_info,
    metadata,
    last_seen_at,
    owner_id,
    visibility
  )
  SELECT
    ws.id,
    'operations-demo-daemon',
    'Operations Demo Runtime',
    'local',
    'codex',
    'online',
    'Seeded local runtime',
    '{"demo":"operations"}'::jsonb,
    now(),
    demo_user.id,
    'public'
  FROM ws, demo_user
  ON CONFLICT (workspace_id, daemon_id, provider) WHERE profile_id IS NULL DO UPDATE SET
    name = EXCLUDED.name,
    status = EXCLUDED.status,
    device_info = EXCLUDED.device_info,
    metadata = EXCLUDED.metadata,
    last_seen_at = EXCLUDED.last_seen_at,
    owner_id = EXCLUDED.owner_id,
    visibility = EXCLUDED.visibility,
    updated_at = now()
  RETURNING id
)
INSERT INTO agent (
  workspace_id,
  name,
  runtime_mode,
  runtime_config,
  visibility,
  status,
  owner_id,
  runtime_id,
  custom_args
)
SELECT
  ws.id,
  'Codex-P4 Demo',
  'local',
  '{"provider":"codex"}'::jsonb,
  'workspace',
  'idle',
  demo_user.id,
  runtime.id,
  '[]'::jsonb
FROM ws, demo_user, runtime
ON CONFLICT (workspace_id, name) DO UPDATE SET
  runtime_mode = EXCLUDED.runtime_mode,
  runtime_config = EXCLUDED.runtime_config,
  visibility = EXCLUDED.visibility,
  status = EXCLUDED.status,
  owner_id = EXCLUDED.owner_id,
  runtime_id = EXCLUDED.runtime_id,
  custom_args = EXCLUDED.custom_args,
  updated_at = now();

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
),
demo_user AS (
  SELECT id FROM "user" WHERE email = 'operations-demo@multica.local'
),
demo_agent AS (
  SELECT a.id
  FROM agent a
  JOIN ws ON ws.id = a.workspace_id
  WHERE a.name = 'Codex-P4 Demo'
)
INSERT INTO issue (
  workspace_id,
  number,
  title,
  description,
  status,
  priority,
  assignee_type,
  assignee_id,
  creator_type,
  creator_id,
  metadata,
  created_at,
  updated_at
)
SELECT
  ws.id,
  r.n,
  r.title,
  'Seeded Operations/P4 assessment demo issue for ' || r.work_item_id || '.',
  r.issue_status,
  'medium',
  'agent',
  demo_agent.id,
  'member',
  demo_user.id,
  jsonb_build_object(
    'demo', true,
    'p4_assessment', true,
    'work_item_id', r.work_item_id,
    'operations_demo', true
  ),
  now() - (r.days_ago || ' days')::interval,
  now() - (r.days_ago || ' days')::interval
FROM operations_demo_rows r, ws, demo_user, demo_agent
ON CONFLICT (workspace_id, number) DO UPDATE SET
  title = EXCLUDED.title,
  description = EXCLUDED.description,
  status = EXCLUDED.status,
  priority = EXCLUDED.priority,
  assignee_type = EXCLUDED.assignee_type,
  assignee_id = EXCLUDED.assignee_id,
  creator_type = EXCLUDED.creator_type,
  creator_id = EXCLUDED.creator_id,
  metadata = EXCLUDED.metadata,
  updated_at = EXCLUDED.updated_at;

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
)
UPDATE workspace
SET issue_counter = GREATEST(
  issue_counter,
  (SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = ws.id)
)
FROM ws
WHERE workspace.id = ws.id;

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
),
demo_agent AS (
  SELECT a.id
  FROM agent a
  JOIN ws ON ws.id = a.workspace_id
  WHERE a.name = 'Codex-P4 Demo'
),
demo_issues AS (
  SELECT i.id
  FROM issue i
  JOIN ws ON ws.id = i.workspace_id
  JOIN operations_demo_rows r ON r.n = i.number
)
DELETE FROM comment c
USING demo_issues di
WHERE c.issue_id = di.id;

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
),
demo_agent AS (
  SELECT a.id
  FROM agent a
  JOIN ws ON ws.id = a.workspace_id
  WHERE a.name = 'Codex-P4 Demo'
)
INSERT INTO comment (
  workspace_id,
  issue_id,
  author_type,
  author_id,
  content,
  type,
  created_at,
  updated_at
)
SELECT
  ws.id,
  i.id,
  'agent',
  demo_agent.id,
  r.agent_comment,
  'comment',
  now() - (r.days_ago || ' days')::interval + interval '30 minutes',
  now() - (r.days_ago || ' days')::interval + interval '30 minutes'
FROM operations_demo_rows r
JOIN ws ON true
JOIN issue i ON i.workspace_id = ws.id AND i.number = r.n
JOIN demo_agent ON true;

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
),
demo_agent AS (
  SELECT a.id
  FROM agent a
  JOIN ws ON ws.id = a.workspace_id
  WHERE a.name = 'Codex-P4 Demo'
),
demo_issues AS (
  SELECT i.id
  FROM issue i
  JOIN ws ON ws.id = i.workspace_id
  JOIN operations_demo_rows r ON r.n = i.number
)
DELETE FROM agent_task_queue atq
USING demo_issues di, demo_agent da
WHERE atq.issue_id = di.id
  AND atq.agent_id = da.id;

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
),
demo_agent AS (
  SELECT a.id, a.runtime_id
  FROM agent a
  JOIN ws ON ws.id = a.workspace_id
  WHERE a.name = 'Codex-P4 Demo'
),
inserted_tasks AS (
  INSERT INTO agent_task_queue (
    agent_id,
    runtime_id,
    issue_id,
    status,
    priority,
    started_at,
    completed_at,
    created_at,
    result,
    context,
    initiator_user_id
  )
  SELECT
    demo_agent.id,
    demo_agent.runtime_id,
    i.id,
    r.task_status,
    0,
    now() - (r.days_ago || ' days')::interval,
    CASE
      WHEN r.task_status IN ('completed', 'failed', 'cancelled') THEN now() - (r.days_ago || ' days')::interval + interval '1 hour'
      ELSE NULL
    END,
    now() - (r.days_ago || ' days')::interval,
    jsonb_build_object('summary', r.agent_comment),
    '{}'::jsonb,
    (SELECT id FROM "user" WHERE email = 'operations-demo@multica.local')
  FROM operations_demo_rows r
  JOIN ws ON true
  JOIN issue i ON i.workspace_id = ws.id AND i.number = r.n
  JOIN demo_agent ON true
  RETURNING id
)
SELECT count(*) FROM inserted_tasks;

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
),
demo_user AS (
  SELECT id FROM "user" WHERE email = 'operations-demo@multica.local'
)
INSERT INTO feishu_project_integration (
  workspace_id,
  project_key,
  plugin_id,
  plugin_secret,
  actor_user_key,
  enabled,
  sync_story,
  sync_issue,
  mql_filter,
  status_mapping,
  reverse_status_mapping,
  work_item_types,
  created_by_id
)
SELECT
  ws.id,
  'OperationsDemo',
  'operations-demo-plugin',
  'operations-demo-secret',
  'operations-demo-bot',
  true,
  false,
  true,
  '',
  '{"Done":"done","In QA":"in_progress"}'::jsonb,
  '{"done":"Done","in_progress":"In QA"}'::jsonb,
  '[{"type_key":"issue","api_name":"issue","name":"缺陷","identifier_prefix":"BUG","project_id":"","status_mapping":{"Done":"done","In QA":"in_progress"},"reverse_status_mapping":{"done":"Done","in_progress":"In QA"}}]'::jsonb,
  demo_user.id
FROM ws, demo_user
ON CONFLICT (workspace_id) DO UPDATE SET
  project_key = EXCLUDED.project_key,
  plugin_id = EXCLUDED.plugin_id,
  plugin_secret = EXCLUDED.plugin_secret,
  actor_user_key = EXCLUDED.actor_user_key,
  enabled = EXCLUDED.enabled,
  sync_story = EXCLUDED.sync_story,
  sync_issue = EXCLUDED.sync_issue,
  mql_filter = EXCLUDED.mql_filter,
  status_mapping = EXCLUDED.status_mapping,
  reverse_status_mapping = EXCLUDED.reverse_status_mapping,
  work_item_types = EXCLUDED.work_item_types,
  created_by_id = EXCLUDED.created_by_id,
  updated_at = now();

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
),
integration AS (
  SELECT fpi.id
  FROM feishu_project_integration fpi
  JOIN ws ON ws.id = fpi.workspace_id
)
INSERT INTO feishu_project_issue_binding (
  workspace_id,
  integration_id,
  issue_id,
  project_key,
  work_item_type,
  work_item_id,
  external_identifier,
  external_url,
  external_status_label,
  external_fields,
  last_external_updated_at,
  last_synced_at
)
SELECT
  ws.id,
  integration.id,
  i.id,
  r.project_key,
  'issue',
  r.work_item_id,
  r.work_item_id,
  'https://meego.example.test/' || r.work_item_id,
  r.external_status,
  jsonb_build_object('version', r.version, 'workstream', r.workstream),
  now() - (r.days_ago || ' days')::interval,
  now()
FROM operations_demo_rows r
JOIN ws ON true
JOIN integration ON true
JOIN issue i ON i.workspace_id = ws.id AND i.number = r.n
ON CONFLICT (workspace_id, issue_id) DO UPDATE SET
  integration_id = EXCLUDED.integration_id,
  project_key = EXCLUDED.project_key,
  work_item_type = EXCLUDED.work_item_type,
  work_item_id = EXCLUDED.work_item_id,
  external_identifier = EXCLUDED.external_identifier,
  external_url = EXCLUDED.external_url,
  external_status_label = EXCLUDED.external_status_label,
  external_fields = EXCLUDED.external_fields,
  last_external_updated_at = EXCLUDED.last_external_updated_at,
  last_synced_at = EXCLUDED.last_synced_at,
  updated_at = now();

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
)
INSERT INTO agent_fix_p4_assessment (
  workspace_id,
  issue_id,
  feishu_binding_id,
  assessment_status,
  delivery_attribution_prediction,
  quality_prediction,
  prediction_reasons,
  confidence,
  workstream,
  swarm_reviews,
  ai_shelved_cls,
  swarm_change_cls,
  swarm_committed_cls,
  external_committed_cls,
  evidence,
  summary,
  warnings,
  model,
  prompt_version,
  assessed_at
)
SELECT
  ws.id,
  i.id,
  fib.id,
  r.assessment_status,
  r.attribution,
  r.quality,
  CASE
    WHEN r.quality = 'likely_correct' THEN ARRAY['complete_usable']::text[]
    WHEN r.quality = 'likely_needs_changes' THEN ARRAY['coverage_incomplete']::text[]
    WHEN r.quality = 'likely_wrong' THEN ARRAY['wrong_direction']::text[]
    ELSE ARRAY[]::text[]
  END,
  r.confidence,
  r.workstream,
  r.swarm_reviews,
  r.ai_shelved_cls,
  r.swarm_change_cls,
  r.swarm_committed_cls,
  r.external_committed_cls,
  jsonb_build_object('work_item_id', r.work_item_id, 'project', r.project_key),
  r.p4_summary,
  r.p4_warnings,
  'gpt-5-codex',
  'p4-assessment-demo-v1',
  CASE
    WHEN r.assessment_status IN ('completed', 'failed', 'stale') THEN now() - (r.days_ago || ' days')::interval + interval '2 hours'
    ELSE NULL
  END
FROM operations_demo_rows r
JOIN ws ON true
JOIN issue i ON i.workspace_id = ws.id AND i.number = r.n
JOIN feishu_project_issue_binding fib ON fib.workspace_id = ws.id AND fib.issue_id = i.id
WHERE r.assessment_status <> 'pending'
ON CONFLICT (workspace_id, feishu_binding_id) DO UPDATE SET
  issue_id = EXCLUDED.issue_id,
  assessment_status = EXCLUDED.assessment_status,
  delivery_attribution_prediction = EXCLUDED.delivery_attribution_prediction,
  quality_prediction = EXCLUDED.quality_prediction,
  prediction_reasons = EXCLUDED.prediction_reasons,
  confidence = EXCLUDED.confidence,
  workstream = EXCLUDED.workstream,
  swarm_reviews = EXCLUDED.swarm_reviews,
  ai_shelved_cls = EXCLUDED.ai_shelved_cls,
  swarm_change_cls = EXCLUDED.swarm_change_cls,
  swarm_committed_cls = EXCLUDED.swarm_committed_cls,
  external_committed_cls = EXCLUDED.external_committed_cls,
  evidence = EXCLUDED.evidence,
  summary = EXCLUDED.summary,
  warnings = EXCLUDED.warnings,
  model = EXCLUDED.model,
  prompt_version = EXCLUDED.prompt_version,
  assessed_at = EXCLUDED.assessed_at,
  updated_at = now();

WITH ws AS (
  SELECT id FROM workspace WHERE slug = 'operations-demo'
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
  reviewed_at
)
SELECT
  ws.id,
  i.id,
  fib.id,
  p4.id,
  r.review_outcome,
  r.review_reasons,
  r.review_note,
  (SELECT id FROM "user" WHERE email = 'operations-demo@multica.local'),
  CASE WHEN r.review_outcome = 'unreviewed' THEN NULL ELSE now() - (r.days_ago || ' days')::interval + interval '3 hours' END
FROM operations_demo_rows r
JOIN ws ON true
JOIN issue i ON i.workspace_id = ws.id AND i.number = r.n
JOIN feishu_project_issue_binding fib ON fib.workspace_id = ws.id AND fib.issue_id = i.id
LEFT JOIN agent_fix_p4_assessment p4 ON p4.workspace_id = ws.id AND p4.feishu_binding_id = fib.id
ON CONFLICT (workspace_id, feishu_binding_id) DO UPDATE SET
  issue_id = EXCLUDED.issue_id,
  p4_assessment_id = EXCLUDED.p4_assessment_id,
  outcome = EXCLUDED.outcome,
  reasons = EXCLUDED.reasons,
  note = EXCLUDED.note,
  reviewer_id = EXCLUDED.reviewer_id,
  reviewed_at = EXCLUDED.reviewed_at,
  updated_at = now();

COMMIT;

SELECT
  'Seeded Operations Demo workspace' AS message,
  w.slug,
  w.issue_prefix,
  count(i.id) AS issues
FROM workspace w
LEFT JOIN issue i ON i.workspace_id = w.id
WHERE w.slug = 'operations-demo'
GROUP BY w.slug, w.issue_prefix;
