---
id: pattern.github.webhook_route_by_repo_registry
kind: pattern
title: "GitHub PR webhooks must route by repository workspace, not installation workspace"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4414
source_pr_number: 4414
source_issue_refs:
  - MUL-3523
merged_at: 2026-06-22T15:44:46Z
merge_commit: ca43c83abc1035834951dbe15ed177b31f36197c
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - server
signals:
  - "PRs never appear under issues for repos that share one GitHub installation across workspaces"
  - "issue pull-requests is empty even when identifiers are present"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/handler/github.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: ca43c83abc1035834951dbe15ed177b31f36197c
    content_hash: c956293b9aff5b37bbd4cd3f91eede1953d98dfb
fix_pattern: "Route webhook side effects by the authoritative repository-to-workspace registry, using the GitHub installation workspace only as fallback."
verification:
  - server/internal/handler/github_test.go
gotchas:
  - "Do not assume installation_id uniquely identifies the correct workspace when one GitHub account owns repos used by multiple workspaces."
  - "This fix does not backfill previously mis-filed PR rows."
related: []
tags:
  - server
  - github
  - workspace-scope
  - webhooks
---

# GitHub PR webhooks must route by repository workspace, not installation workspace

## 症状

PR to issue auto-linking silently failed for a workspace when one GitHub account or app installation owned repositories used by multiple Multica workspaces. PR rows were filed under the wrong workspace and identifiers with the real workspace prefix did not link.

## 根因

`github_installation.installation_id` maps to a single workspace and the last workspace to connect the installation can win. Both pull request and check suite handlers used the installation's workspace to upsert PR rows and resolve issue prefixes, so repositories registered to another workspace were scanned against the wrong prefix.

## 正确修复模式

Resolve the workspace from the repository owner/name using the existing `workspace.repos` registry. Use the installation workspace only as fallback when the repo is not registered anywhere. Apply the same routing to both PR and check suite webhook paths.

## 为什么这个修法正确

The repository registry is the authoritative product mapping for which workspace owns a repo. GitHub installation identity is still useful for receiving and authenticating webhooks, but it is not precise enough to route workspace-scoped issue linking.

## 验证方式

The PR added a repo slug parser test and an integration test where a PR delivered through an installation mapped to workspace A links to an issue in workspace B because workspace B registered the repo.

## 适用边界

Use this pattern for workspace-scoped integrations where one external account can span multiple Multica workspaces.

## 不要照搬

Do not change webhook authentication to ignore installations. The installation remains the receipt/auth fallback; only workspace side effects are routed by repo registry.
