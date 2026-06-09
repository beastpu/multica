---
name: multica-upstream-sync
description: Use when syncing Multica upstream code, merging upstream main, resolving upstream/fork/internal GitLab differences, handling Lilith fork conflicts, checking local-only deployment files, or discussing official upstream GitHub, beastpu fork, and internal GitLab repository flow.
---

# Multica Upstream Sync

Use this skill for syncing upstream Multica code into Lilith's internal fork.

## Repository Chain

The repository sync relationship must always be:

```text
official upstream GitHub -> upstream fork repository -> internal GitLab repository
```

Concrete repositories:

- Official upstream GitHub: `https://github.com/multica-ai/`
- Upstream fork repository: `https://github.com/beastpu/multica`
- Internal private GitLab main repository: `https://gitlab.lilithgame.com/devops/multica`

The local repo is the internal private GitLab fork unless proven otherwise.

## Read First

Before planning or executing a sync, read:

- `README.md`, especially "与上游的关系".
- `CONTRIBUTING.md`, especially the internal contribution and scope guidance if touching code.
- `deploy/k8s/README.md` for deployment overlays.
- `deploy/k8s/base/ingress.yaml` before resolving infra conflicts.

## Core Rules

- Keep the sync direction: official upstream GitHub -> `beastpu/multica` -> internal GitLab.
- Do not sync internal GitLab directly from official upstream unless the user explicitly overrides the chain.
- Internal Lilith changes should primarily extend rather than rewrite upstream behavior.
- Changes that are suitable for upstream should also be proposed as upstream PRs; after upstream merges, the local diff can disappear naturally.
- Expect conflicts in Lilith-only deployment files; do not blindly take upstream.

## Local-Only / High-Risk Files

The following files contain Lilith deployment-specific behavior and are expected to conflict or drift during upstream sync:

- `deploy/k8s/base/ingress.yaml`
- `deploy/k8s/base/albconfig.yaml`
- `deploy/k8s/base/configmap.yaml`
- `deploy/k8s/overlays/test/`
- `deploy/k8s/overlays/prod/`
- `apps/desktop/electron-builder.yml`
- `apps/desktop/src/main/updater.ts`
- `server/internal/handler/downloads.go`
- `server/cmd/server/router.go`
- `apps/web/app/(landing)/download/page.tsx`
- `apps/web/features/landing/utils/github-release.ts`

For `deploy/k8s/base/ingress.yaml`, the correct conflict resolution is almost always to keep Lilith's additions. It is local-only and has no direct upstream counterpart. Preserve:

- ACK ALB annotations and ACL wiring.
- `multica.lilithgames.com` canonical host.
- `ship.lilithgames.com` 301 redirect.
- `/webhook/meego` route to `multica-feishu-bridge`.
- `/doc` route to `multica-feishu-bridge`, especially `/doc/install.md`.
- OAuth callback routes that must go to `multica-web`.

Known incident to remember: `/doc/install.md` routing was once lost during an upstream-sync revert chain. Always verify it survives syncs.

## Desktop Download / Update Must Stay Lilith-Hosted

Lilith desktop downloads and auto-updates must point at Lilith's own service, not GitHub download endpoints.

Preserve this behavior during upstream sync:

- Desktop updater provider is `generic` and points at `https://multica.lilithgames.com/api/downloads`.
- The Go server proxies `/api/downloads/<file>` from private Aliyun OSS via `server/internal/handler/downloads.go`.
- Release installers and `latest-*.yml` metadata live under OSS `downloads/`.
- The public web download page should use the Lilith-hosted download service path, not upstream GitHub release assets.
- Do not accept upstream changes that make desktop clients or the web download page fetch installers directly from GitHub unless the user explicitly asks to abandon Lilith-hosted downloads.

## Suggested Sync Workflow

Inspect remotes and branches first:

```bash
git remote -v
git status --short --branch
git branch --show-current
```

Confirm the remotes map to the required chain. If remotes are missing or ambiguous, stop and ask before changing them.

Fetch explicitly:

```bash
git fetch --all --prune
```

Before merging, ensure the worktree is clean or that existing user changes are unrelated and understood:

```bash
git status --short
```

Merge from the upstream fork branch that already follows official upstream. The exact remote/branch name may vary, so inspect remotes instead of guessing.

After resolving conflicts, check the likely regression points:

```bash
rg -n "/doc|install.md|webhook/meego|ship-to-multica|multica-feishu-bridge|alb.ingress.kubernetes.io" deploy/k8s/base deploy/k8s/overlays
rg -n "github.com|githubusercontent|releases/download|api/downloads|multica.lilithgames.com/api/downloads|provider: generic|DOWNLOADS_OSS" apps/desktop apps/web server
git diff -- deploy/k8s/base/ingress.yaml deploy/k8s/base/configmap.yaml deploy/k8s/base/albconfig.yaml
git diff -- apps/desktop/electron-builder.yml apps/desktop/src/main/updater.ts server/internal/handler/downloads.go server/cmd/server/router.go 'apps/web/app/(landing)/download/page.tsx' apps/web/features/landing/utils/github-release.ts
```

Run targeted checks only when appropriate for the changed surface; full verification is expensive and should be run when requested or before finalizing a code-changing sync:

```bash
pnpm typecheck
pnpm test
make test
make check
```

## Conflict Resolution Heuristics

- Product/frontend/backend conflicts: compare intent; prefer upstream when Lilith has no deliberate local extension.
- Deployment conflicts: prefer Lilith local overlay unless upstream contains a deliberate security or compatibility fix that must be ported.
- Download/update conflicts: preserve Lilith-hosted `/api/downloads` and Aliyun OSS proxy behavior; do not regress to GitHub release download URLs.
- API or database migration conflicts: inspect both sides carefully; preserve forward migration order and run `make sqlc` after query changes.
- Built-in skill behavior changes: if a CLI command, API field, or shipped skill behavior changes, update `server/internal/service/builtin_skills/*` and source maps as required by `CLAUDE.md`.

## Final Report

When reporting a sync result, include:

- Source remote/branch and target branch.
- Whether deployment local-only files changed.
- Any conflicts and how they were resolved.
- Checks run and failures, if any.
- Follow-up upstream PR candidates, if obvious.
