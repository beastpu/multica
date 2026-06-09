---
name: multica-prod-debug
description: Use when debugging Multica production or test environment problems via local kubeconfig, ACK, Kubernetes namespaces multica or multica-test, queued/running agent tasks, daemon/runtime issues, pod logs, RDS/database investigation, psql-debug, Feishu bridge, ALB ingress, or multica.lilithgames.com incidents.
---

# Multica Prod/Test Debug

Use this skill for operational debugging of Lilith Multica in ACK.

## Environment Map

- One ACK cluster contains both environments.
- Production namespace: `multica`
- Test namespace: `multica-test`
- Local kubeconfig can access the cluster directly.
- Production entrypoint: `https://multica.lilithgames.com`
- Test/pre-prod is the `multica-test` namespace and matching overlay under `deploy/k8s/overlays/test`.
- Database investigation should go through the PostgreSQL debug pod in the `multica` namespace, usually `deploy/psql-debug`, which has `psql` and `DATABASE_URL`.

Before debugging, read only the relevant local docs:

- `README.md` for upstream/fork/deployment overview.
- `deploy/k8s/README.md` for ACK deployment layout and rollout workflow.
- `deploy/k8s/overlays/test/README.md` when the issue is in test/pre-prod.

## Safety Rules

- Treat all cluster and database access as production-sensitive.
- Prefer read-only commands unless the user explicitly asks for a fix.
- Do not print secret values, database URLs, tokens, cookies, or env secrets.
- If inspecting env, use `kubectl describe` or redact values manually.
- Never mutate database rows to "unstick" a task unless the user explicitly approves the exact action.
- When using SQL, start with `SELECT`; explain any proposed `UPDATE` or `DELETE` before running it.

## First Checks

```bash
kubectl config current-context
kubectl get ns multica multica-test
kubectl get pods -n multica -o wide
kubectl get pods -n multica-test -o wide
kubectl get deploy,svc,ingress -n multica
kubectl get deploy,svc,ingress -n multica-test
```

For logs:

```bash
kubectl logs -n multica deploy/multica-server --since=2h --all-containers=true
kubectl logs -n multica deploy/multica-web --since=2h --all-containers=true
kubectl logs -n multica deploy/multica-feishu-bridge --since=2h --all-containers=true
```

Use `rg` locally on log output for IDs, issue numbers, task IDs, runtime IDs, `claim`, `queued`, `running`, `offline`, `heartbeat`, `feishu`, `meego`, `webhook`, and `error`.

## Database Access

Use the debug pod in `multica`:

```bash
kubectl exec -n multica deploy/psql-debug -- sh -c 'psql "$DATABASE_URL" -P pager=off'
```

If unsure about production column names, inspect the schema first:

```sql
\d issue
\d agent_task_queue
\d agent
\d agent_runtime
\d workspace
\d "user"
```

## Queued Issue Runbook

When a user asks why an issue is "queued" or not being picked up, extract the issue UUID or workspace/issue number from the URL, then query:

```sql
\x on
SELECT i.id, i.workspace_id, w.slug AS workspace_slug, i.number, i.title,
       i.status, i.priority, i.assignee_type, i.assignee_id,
       i.creator_type, i.creator_id, i.first_executed_at,
       i.created_at, i.updated_at, i.origin_type, i.origin_id, i.metadata
FROM issue i
LEFT JOIN workspace w ON w.id = i.workspace_id
WHERE i.id = '<issue_uuid>';

SELECT q.id, q.agent_id, a.name AS agent_name,
       q.runtime_id, r.name AS runtime_name, r.status AS runtime_status,
       r.last_seen_at, r.owner_id AS runtime_owner_id,
       q.status, q.priority, q.attempt, q.max_attempts, q.wait_reason,
       q.created_at, q.dispatched_at, q.started_at, q.completed_at,
       q.error, q.failure_reason, q.parent_task_id, q.trigger_comment_id
FROM agent_task_queue q
LEFT JOIN agent a ON a.id = q.agent_id
LEFT JOIN agent_runtime r ON r.id = q.runtime_id
WHERE q.issue_id = '<issue_uuid>'
ORDER BY q.created_at DESC;

SELECT a.id, a.name, a.status, a.runtime_mode, a.runtime_id,
       a.max_concurrent_tasks, a.owner_id AS agent_owner_id,
       r.name AS runtime_name, r.status AS runtime_status,
       r.last_seen_at, r.daemon_id, r.owner_id AS runtime_owner_id,
       r.provider, r.runtime_mode AS runtime_mode_actual
FROM issue i
JOIN agent a ON i.assignee_id = a.id
LEFT JOIN agent_runtime r ON a.runtime_id = r.id
WHERE i.id = '<issue_uuid>' AND i.assignee_type = 'agent';
```

Interpretation:

- `agent_task_queue.status = queued` with `agent_runtime.status = offline`: waiting for that local runtime/daemon to reconnect.
- `queued` with online runtime: inspect server logs for the runtime ID and task ID; check whether tasks are ahead in the same runtime queue.
- `dispatched` or `running` with stale timestamps: inspect daemon status calls and task messages before proposing cleanup.
- Agent status alone is not enough; runtime status and `last_seen_at` usually explain queue pickup.

To see queue pressure for a runtime:

```sql
SELECT status, count(*)
FROM agent_task_queue
WHERE runtime_id = '<runtime_uuid>'
GROUP BY status
ORDER BY status;

SELECT q.id, q.issue_id, i.number, i.title, q.status, q.priority,
       q.created_at, q.dispatched_at, q.started_at, q.completed_at,
       q.error, q.failure_reason
FROM agent_task_queue q
LEFT JOIN issue i ON i.id = q.issue_id
WHERE q.runtime_id = '<runtime_uuid>'
ORDER BY q.created_at DESC
LIMIT 20;
```

## Deployment/Rollout Pointers

ACK manifests live under `deploy/k8s/`.

- Shared resources: `deploy/k8s/base/`
- Test overlay: `deploy/k8s/overlays/test/`
- Prod overlay: `deploy/k8s/overlays/prod/`
- Prod uses Aliyun ACK + ALB Ingress + Aliyun RDS PostgreSQL.
- The daemon runs outside the cluster and dials into the server; there is no daemon Deployment.

Useful rollout checks:

```bash
kubectl -n multica rollout status deployment/multica-server
kubectl -n multica rollout status deployment/multica-web
kubectl -n multica logs job/multica-migrate
kubectl -n multica get ingress multica
```

Use `multica-test` instead of `multica` for test/pre-prod.
