---
id: pattern.skills.private_github_raw_download_auth
kind: pattern
title: "Private GitHub skill imports need auth on raw.githubusercontent.com downloads"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4389
source_pr_number: 4389
source_issue_refs:
  - "#4385"
  - MUL-3496
  - https://github.com/multica-ai/multica/issues/4385
merged_at: 2026-06-22T05:34:06Z
merge_commit: 39ab35558569cf622269252a6c53b8b7e120465e
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - server
signals:
  - "Private/internal GitHub skill import succeeds at listing but fails during raw file download"
  - "CLI surfaces a generic 502 server error for private repo import"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/handler/skill.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 39ab35558569cf622269252a6c53b8b7e120465e
    content_hash: 56e0ca6926b55d8bbc1db46427126fbb5203d416
fix_pattern: "When an integration has both API and raw-content fetch paths, apply existing authentication to every first-party GitHub path, but gate token attachment by trusted host so credentials are not sent to third-party import sources."
verification:
  - server/internal/handler/skill_test.go
gotchas:
  - "Do not add the GitHub bearer token unconditionally to all fetchRawFile hosts."
  - "The PR intentionally did not change the generic 403/404 to 502 error mapping."
related: []
tags:
  - server
  - skills
  - github
  - auth
---

# Private GitHub skill imports need auth on raw.githubusercontent.com downloads

## 症状

Importing a skill from a private or internal GitHub repository failed even when `GITHUB_TOKEN` was set. The directory listing path could succeed, but downloading the actual raw files failed and surfaced as a generic 502.

## 根因

The skill importer constructed `raw.githubusercontent.com` URLs and downloaded them through `fetchRawFile`, which used a plain unauthenticated GET. Earlier GitHub authentication only covered the `api.github.com` path, so the raw-content path missed the token and private raw downloads returned 404.

## 正确修复模式

Attach the existing GitHub bearer token to raw GitHub downloads as well as API requests, but only when the URL host is `raw.githubusercontent.com`. Keep the host gate in a testable helper so the credential boundary is explicit.

## 为什么这个修法正确

The same import flow spans two GitHub surfaces: API listing and raw content retrieval. Both must be authenticated for private repos. The host gate preserves the security boundary because `fetchRawFile` is also shared with `clawhub.ai` and `skills.sh` downloads.

## 验证方式

The PR added tests covering the positive case where `raw.githubusercontent.com` receives `Bearer <token>`, third-party hosts do not receive the token, and no header is attached when `GITHUB_TOKEN` is unset.

## 适用边界

Use this pattern when a feature has separate metadata and content fetch paths. Verify each path's auth behavior independently.

## 不要照搬

Do not leak GitHub tokens to arbitrary URLs merely because a helper is used by GitHub imports in one call path.
