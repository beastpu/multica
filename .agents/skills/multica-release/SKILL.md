---
name: multica-release
description: Multica release and deployment SOP for Lilith internal GitLab/ACK. Use when asked to merge develop to main, create or verify release tags, build and push multica-server/multica-web images, update multica-test or multica production, run migrations, roll Kubernetes deployments, patch release config, promote test to prod, or debug release fallout such as missing runtime env vars.
---

# Multica Release

Release Multica through the internal GitLab repository and ACK namespaces while preserving live production configuration.

## Release Map

- Git remote: `gitlab` -> `https://gitlab.lilithgame.com/devops/multica.git`.
- Test namespace: `multica-test`, public host `https://multica-test.lilithgames.com`.
- Production namespace: `multica`, public host `https://multica.lilithgames.com`.
- Registry: `lilith-registry.cn-shanghai.cr.aliyuncs.com/devops`.
- Images: `multica-server:<tag>` and `multica-web:<tag>`.
- Current local checkout may be dirty or on an unrelated branch. Use a clean detached worktree for builds and merges.
- Do not run or list formal Tide production pipelines unless the user explicitly requests a pipeline flow and confirms dynamic/source parameters.

## Hard Guards

- Never print secrets, tokens, database URLs, cookies, AK/SK, or decoded Secret values.
- Do not use `kubectl apply -k deploy/k8s/overlays/prod` for routine production image updates. Live prod ConfigMap/Secret may contain extra keys; use `kubectl set image` or targeted `kubectl patch`.
- Do not overwrite user changes in the current checkout. Keep release work in `/Users/lilithgames/multica-release-<tag>` or `/Users/lilithgames/multica-test-<tag>`.
- Always run migrations before rolling server/web when the server image changes, even if logs later show all migrations already applied.
- For config fixes, patch only the exact ConfigMap/Secret key required and roll only affected deployments.
- For default Feishu Project plugin credentials, `FEISHU_PROJECT_DEFAULT_PLUGIN_ID` and `FEISHU_PROJECT_DEFAULT_PLUGIN_SECRET` must exist as a pair. Usually ID is in ConfigMap and secret is in Secret.

## Develop To Main And Tag

1. Fetch:

```bash
git fetch gitlab main develop --tags --prune
```

2. Check tag availability and divergence:

```bash
git tag --list '0.2*' --sort=-version:refname | head -30
git ls-remote --tags gitlab 'refs/tags/<tag>'
git log --oneline --left-right --cherry-pick --max-count=80 gitlab/main...gitlab/develop
```

3. Merge in a worktree:

```bash
git worktree add -b codex/merge-develop-main-<tag> /Users/lilithgames/multica-main-release-<tag> gitlab/main
cd /Users/lilithgames/multica-main-release-<tag>
git merge --no-ff gitlab/develop -m "Merge branch 'develop' into 'main'"
```

4. Run targeted checks for touched areas. Typical checks:

```bash
cd server && go test ./internal/service ./internal/handler
pnpm install --frozen-lockfile
pnpm --filter @multica/views typecheck
pnpm --filter @multica/core typecheck
pnpm --filter @multica/views exec vitest run <changed-view-tests>
```

5. Push to `main`. If GitLab rejects protected branch pushes, push an MR branch:

```bash
git push gitlab HEAD:main
git push -u gitlab codex/merge-develop-main-<tag> \
  -o merge_request.create \
  -o merge_request.target=main \
  -o merge_request.title="Merge develop into main for <tag>" \
  -o merge_request.remove_source_branch
```

6. Only after the merge is actually in `gitlab/main`, tag main:

```bash
git fetch gitlab main --tags --prune
git merge-base --is-ancestor gitlab/develop gitlab/main
git tag <tag> gitlab/main
git push gitlab refs/tags/<tag>
git ls-remote --tags gitlab "refs/tags/<tag>"
```

## Build Images

Use the exact tag or SHA requested. Build from a detached worktree:

```bash
git worktree add --detach /Users/lilithgames/multica-release-<tag> <tag-or-commit>
cd /Users/lilithgames/multica-release-<tag>
```

Build server:

```bash
TAG=<tag>
COMMIT=$(git rev-parse HEAD)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
docker buildx build --platform linux/amd64 -f Dockerfile \
  -t lilith-registry.cn-shanghai.cr.aliyuncs.com/devops/multica-server:${TAG} \
  --build-arg VERSION=${TAG} \
  --build-arg COMMIT=${COMMIT} \
  --build-arg DATE=${DATE} \
  --push .
```

Build web. Use the public Feishu App ID from the target namespace ConfigMap; do not print secrets.

```bash
FEISHU_APP_ID=$(kubectl -n <namespace> get configmap multica-config -o jsonpath='{.data.NEXT_PUBLIC_FEISHU_APP_ID}')
docker buildx build --platform linux/amd64 -f Dockerfile.web \
  -t lilith-registry.cn-shanghai.cr.aliyuncs.com/devops/multica-web:${TAG} \
  --build-arg REMOTE_API_URL=http://multica-server:8080 \
  --build-arg NEXT_PUBLIC_FEISHU_APP_ID=${FEISHU_APP_ID} \
  --build-arg NEXT_PUBLIC_APP_VERSION=${TAG} \
  --push .
```

Verify manifests:

```bash
crane manifest lilith-registry.cn-shanghai.cr.aliyuncs.com/devops/multica-server:${TAG} | jq -r '.mediaType, (.manifests[]? | [.platform.os, .platform.architecture, .digest] | @tsv)'
crane manifest lilith-registry.cn-shanghai.cr.aliyuncs.com/devops/multica-web:${TAG} | jq -r '.mediaType, (.manifests[]? | [.platform.os, .platform.architecture, .digest] | @tsv)'
```

Expect `linux amd64`. `unknown unknown` attestation entries are normal.

## Deploy To Test

Deploy both images to `multica-test` unless the user asks for backend-only or frontend-only.

Run migration:

```bash
TAG=<tag>
kubectl -n multica-test delete job multica-migrate --ignore-not-found
kubectl apply -f - <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: multica-migrate
  namespace: multica-test
  labels:
    app.kubernetes.io/name: multica
    app.kubernetes.io/component: migrate
  annotations:
    multica.lilithgames.com/image-tag: "${TAG}"
spec:
  ttlSecondsAfterFinished: 3600
  backoffLimit: 2
  template:
    metadata:
      labels:
        app.kubernetes.io/name: multica
        app.kubernetes.io/component: migrate
    spec:
      restartPolicy: Never
      imagePullSecrets:
        - name: regcred
      containers:
        - name: migrate
          image: lilith-registry.cn-shanghai.cr.aliyuncs.com/devops/multica-server:${TAG}
          command: ["./migrate", "up"]
          envFrom:
            - configMapRef:
                name: multica-config
            - secretRef:
                name: multica-secrets
EOF
kubectl -n multica-test wait --for=condition=complete job/multica-migrate --timeout=5m
kubectl -n multica-test logs job/multica-migrate --tail=100
```

Roll deployments:

```bash
kubectl -n multica-test set image deployment/multica-server server=lilith-registry.cn-shanghai.cr.aliyuncs.com/devops/multica-server:${TAG}
kubectl -n multica-test set image deployment/multica-web web=lilith-registry.cn-shanghai.cr.aliyuncs.com/devops/multica-web:${TAG}
kubectl -n multica-test rollout status deployment/multica-server --timeout=10m
kubectl -n multica-test rollout status deployment/multica-web --timeout=10m
```

Smoke checks:

```bash
kubectl -n multica-test get deploy multica-server multica-web -o 'custom-columns=NAME:.metadata.name,READY:.status.readyReplicas,UPDATED:.status.updatedReplicas,AVAILABLE:.status.availableReplicas,IMAGE:.spec.template.spec.containers[0].image'
kubectl -n multica-test exec deploy/multica-server -- wget -qO- http://127.0.0.1:8080/readyz
curl -fsS -o /tmp/multica-test-config.json -w 'api_config_http=%{http_code}\n' https://multica-test.lilithgames.com/api/config
curl -fsS -o /tmp/multica-test-home.html -w 'home_http=%{http_code} bytes=%{size_download}\n' https://multica-test.lilithgames.com/
```

## Deploy To Production

Use the same migration and rollout sequence with namespace `multica` and host `https://multica.lilithgames.com`.

Prefer:

```bash
kubectl -n multica set image deployment/multica-server server=lilith-registry.cn-shanghai.cr.aliyuncs.com/devops/multica-server:${TAG}
kubectl -n multica set image deployment/multica-web web=lilith-registry.cn-shanghai.cr.aliyuncs.com/devops/multica-web:${TAG}
```

For backend-only releases, build/push only `multica-server`, run migration if the server image changed, and roll only `deployment/multica-server`. For frontend-only releases, build/push only `multica-web` and roll only `deployment/multica-web`.

Production smoke checks:

```bash
kubectl -n multica get deploy multica-server multica-web -o 'custom-columns=NAME:.metadata.name,READY:.status.readyReplicas,UPDATED:.status.updatedReplicas,AVAILABLE:.status.availableReplicas,IMAGE:.spec.template.spec.containers[0].image'
kubectl -n multica exec deploy/multica-server -- wget -qO- http://127.0.0.1:8080/readyz
curl -fsS -o /tmp/multica-prod-config.json -w 'api_config_http=%{http_code}\n' https://multica.lilithgames.com/api/config
curl -fsS -o /tmp/multica-prod-home.html -w 'home_http=%{http_code} bytes=%{size_download}\n' https://multica.lilithgames.com/
```

## Targeted Config Patches

Use targeted patches for live config drift. Do not re-apply the whole production overlay.

Check presence without printing secrets:

```bash
kubectl -n <namespace> get configmap multica-config -o json | jq -r '.data | {has_plugin_id: has("FEISHU_PROJECT_DEFAULT_PLUGIN_ID"), plugin_id_nonempty: ((.FEISHU_PROJECT_DEFAULT_PLUGIN_ID // "") | length > 0)}'
kubectl -n <namespace> get secret multica-secrets -o json | jq -r '.data | {has_plugin_secret: has("FEISHU_PROJECT_DEFAULT_PLUGIN_SECRET"), plugin_secret_nonempty: ((.FEISHU_PROJECT_DEFAULT_PLUGIN_SECRET // "") | length > 0)}'
kubectl -n <namespace> exec deploy/multica-server -- sh -c 'printf "id_set=%s secret_set=%s\n" "$([ -n "${FEISHU_PROJECT_DEFAULT_PLUGIN_ID:-}" ] && echo yes || echo no)" "$([ -n "${FEISHU_PROJECT_DEFAULT_PLUGIN_SECRET:-}" ] && echo yes || echo no)"'
```

Example: copy the Feishu Project default plugin ID from test to prod without printing it:

```bash
plugin_id=$(kubectl -n multica-test get configmap multica-config -o jsonpath='{.data.FEISHU_PROJECT_DEFAULT_PLUGIN_ID}')
test -n "$plugin_id"
kubectl -n multica patch configmap multica-config --type merge --patch "$(jq -n --arg id "$plugin_id" '{data:{FEISHU_PROJECT_DEFAULT_PLUGIN_ID:$id}}')"
kubectl -n multica rollout restart deployment/multica-server
kubectl -n multica rollout status deployment/multica-server --timeout=10m
```

After a config patch, verify env and check recent logs:

```bash
kubectl -n multica exec deploy/multica-server -- sh -c 'printf "id_set=%s secret_set=%s\n" "$([ -n "${FEISHU_PROJECT_DEFAULT_PLUGIN_ID:-}" ] && echo yes || echo no)" "$([ -n "${FEISHU_PROJECT_DEFAULT_PLUGIN_SECRET:-}" ] && echo yes || echo no)"'
kubectl -n multica logs -l app.kubernetes.io/name=multica,app.kubernetes.io/component=server --since=5m --all-containers=true --prefix=true | rg -n "no plugin credentials|FEISHU_PROJECT_DEFAULT_PLUGIN|plugin credentials|plugin_id is required|plugin_secret is required" | tail -40 || true
```

## Desktop Release Notes

Semver tags like `0.2.61` are mirrored from GitLab to GitHub and trigger the Lilith desktop release workflow in `CopilotDemo/multica`. Suffix tags may be accepted by GitHub Actions but are not accepted by the current GitLab mirror rule, so use plain semver when the desktop release pipeline must run.

Check mirror/workflow:

```bash
git ls-remote --tags https://github.com/CopilotDemo/multica.git "refs/tags/<tag>"
gh run list --repo CopilotDemo/multica --workflow 'Lilith desktop release' --limit 3 --json databaseId,status,conclusion,event,headSha,headBranch,displayTitle,createdAt,url
```

## Cleanup

After builds and deploys:

```bash
git worktree remove /Users/lilithgames/multica-release-<tag> --force
git worktree prune
```

Do not delete user-owned feature worktrees. Only remove the temporary worktree created for the release task.
