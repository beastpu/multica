---
name: multica-release
description: Multica release and deployment SOP for Lilith internal GitLab/ACK. Use when asked to merge develop to main, create or verify release tags, build and push multica-server/multica-web images, update multica-test or multica production, run migrations, roll Kubernetes deployments, patch release config, promote test to prod, ship a daemon or CLI change to the cloud-runtime nodes, or debug release fallout such as missing runtime env vars or a node running an old multica CLI.
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
- Deploying the server does not update the runtime nodes. A change under `server/internal/daemon/` or `server/cmd/multica/` reaches an agent only through the chain in **Runtime Node CLI**; without it, a server deploy proves nothing about agent behaviour. Check before concluding a deploy is done.

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

## Runtime Node CLI

The `multica` binary an agent runs is not the one in the server image. Nodes
install it from OSS, pinned by cloud-runtime's `runtimes/catalog.json`. So a
server deploy leaves every node exactly as it was, and a daemon or CLI change
argued from a server deploy is argued from nothing.

Decide whether the chain is needed by diffing the two paths that ship to nodes:

```bash
git diff --stat <deployed-commit> <new-commit> -- server/internal/daemon/ server/cmd/multica/
```

Empty output means the node's CLI is behaviourally identical and nothing below
is required. Any output means the node is now behind the server.

### The chain

Four steps, in order. Stopping after any of them leaves the node unchanged.

1. **Publish the CLI.** multica CI's `publish-cli` job writes
   `downloads/multica-cli-<tag>-linux-<arch>.tar.gz` and the channel pointer
   `latest-cli-test.txt`. It runs on `main` or on a web/api-triggered pipeline,
   not on an ordinary branch push. Confirm what landed:

```bash
curl -sS https://multica.lilithgames.com/api/downloads/latest-cli-test.txt
```

2. **Pin it in cloud-runtime.** The catalog pins a version, two URLs and two
   digests; it does not follow the pointer. In a clone of
   `https://gitlab.lilithgame.com/devops/cloud-runtime.git`:

```bash
./scripts/sync-multica-cli.sh --channel test --dry-run
./scripts/sync-multica-cli.sh --channel test
```

   Commit the 5-insertion/5-deletion diff on a branch, open an MR, merge. The
   `workflow.rules` there only build `main`, tags and manual pipelines, so an
   unmerged branch publishes nothing.

3. **Read the published revision** out of the `publish-runtime-tools` job log —
   `RUNTIME_TOOLS_REVISION` and `RUNTIME_TOOLS_SHA256`. The tools tarball is
   large enough that the job's CI artifact upload is the part most likely to
   fail; the OSS upload and its signed-URL check are what matter, and they are
   logged separately. A red job whose `✅ signed URL fetches` line is present
   has published correctly.

4. **Point the node at that revision** (see below), then restart it. The sync
   runs as an initContainer, so the pod must roll for it to take effect.

### Which fleet the channel moves

`--channel test` does not mean "the test nodes". There is one
`runtimes/catalog.json` and one `multica` version in it, so the channel decides
what the **next package built for anyone** contains. Read it as "point the
catalog at a branch build", not as an environment.

```bash
curl -sS https://multica.lilithgames.com/api/downloads/latest-cli.txt       # release: tag builds
curl -sS https://multica.lilithgames.com/api/downloads/latest-cli-test.txt  # test: branch builds
jq -r '.components[] | select(.id=="multica") | .version' runtimes/catalog.json
```

Three things could separate test from production, and only the last one does:

- **Channel.** One catalog, one value. Not a separation.
- **Rollout scope.** `runtime-tools-rollout.sh --mode canary|stable` selects on
  `multica.lilithgames.com/update-channel` and `...=/runtime-profile`. No node
  in the fleet carries either label today, so both modes select nothing and
  `--mode target <ns>/<name>` is the only one that acts. Check before relying
  on them:

```bash
kubectl get statefulset -A -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name,CHANNEL:.metadata.labels.multica\.lilithgames\.com/update-channel
```

- **The per-node pin.** The three initContainer variables. A node moves when it
  is patched and not before, so the isolation in practice is that only the nodes
  you patch — and only the nodes that have `runtime-tools-sync` at all — ever
  see a new CLI.

That last line is the whole safety margin, and it is a property of the current
fleet rather than of the design. Before adding `runtime-tools-sync` to a
production node, check what the catalog holds: if a branch build is pinned, that
node inherits it on its first sync. Move the catalog back first.

```bash
./scripts/sync-multica-cli.sh --channel release      # back to the tag builds
./scripts/sync-multica-cli.sh --version <known-good> # or an exact version
```

The same seam catches the other way round: resolving without `--pinned` reads
`latest-cli.txt`, which silently replaces a test build under test with the
released one.

### Patching a node

Get the cluster: the cloud-runtime kubeconfig lives in srt as
`cloud-runtime-kubeconfig`, field `content`. Resolve it inside a child process
and never write it anywhere durable:

```bash
srt secrets env cloud-runtime-kubeconfig content --var KUBECONFIG_CONTENT
srt run --env-file <file> -- sh -c '
KC=$(mktemp); chmod 600 "$KC"; printf "%s" "$KUBECONFIG_CONTENT" > "$KC"; export KUBECONFIG="$KC"
kubectl get statefulset -A | grep -E "node-"
rm -f "$KC"'
```

Patch only the three variables that name the revision. Do not use
`scripts/runtime-tools-rollout.sh` without reading what it writes: it assumes
the workspace directory is the namespace name and the app container is called
`multica`, and a node onboarded with a workspace-UUID directory and a container
called `runtime` gets its `HOME` and mount rewritten and a second, imageless
container added.

```bash
kubectl -n <ns> patch sts <node> --patch-file <(cat <<'EOF'
{"spec":{"template":{"spec":{"initContainers":[{"name":"runtime-tools-sync","env":[
{"name":"MULTICA_TOOLS_REVISION","value":"<revision>"},
{"name":"MULTICA_TOOLS_URL","value":"oss://<bucket>/multica-cloud-runtime-tools/<revision>/runtime-tools.tar.gz"},
{"name":"MULTICA_TOOLS_SHA256","value":"<sha256>"}]}]}}}}
EOF
) --dry-run=server -o jsonpath='{range .spec.template.spec.initContainers[0].env[*]}{.name}={.value}{"\n"}{end}'
```

Run the server-side dry-run first and confirm `HOME` and `MULTICA_WORKSPACE`
come back unchanged. Then apply and `rollout status`.

### PATH, and why the sync can succeed while changing nothing

The tools land in `$HOME/.runtime-tools/current/`, and the app container only
uses them if its `PATH` puts that ahead of `/usr/local/bin`. Without it the
initContainer logs `activated runtime tools revision <rev>`, the pod is healthy,
CI is green, and the node keeps running the CLI baked into its image.

The `PATH` entry belongs on the app container and must be a literal path.
Kubernetes expands `$(VAR)` only against variables defined **earlier in the same
container's env list**, and a strategic-merge patch prepends — so
`/workspace/$(MULTICA_WORKSPACE)/...` lands unexpanded and matches nothing.

```json
{"spec":{"template":{"spec":{"containers":[{"name":"runtime","env":[{"name":"PATH","value":"/workspace/<workspace-uuid>/.runtime-tools/current/npm-global/bin:/workspace/<workspace-uuid>/.runtime-tools/current/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}]}]}}}}
```

### Profiles decide which CLIs exist

`runtimes/catalog.json` defines `codex`, `claude`, `pi` and `all`; the profile
selects which components the tools package installs, and the agent CLIs are
installed by the package — the image ships none of them. A node on the `claude`
profile has no `codex` binary, which surfaces only as a runtime that reads as
offline, with nothing in any log. `RUNTIME_PROFILE` in cloud-runtime's
`.gitlab-ci.yml` sets this for the whole fleet; a node needs `all` to serve both.

### Verify on the node, not in the logs

The sync fails soft by design: any failure falls back to the image's tools and
exits 0. The only evidence that a node updated is the node.

```bash
kubectl -n <ns> exec <pod> -c runtime -- sh -c 'command -v multica; multica --version'
```

`command -v` must resolve under `.runtime-tools/current/`, and the version must
be the tag just published. Use `sh -c`, not `sh -lc`: a login shell re-reads
`/etc/profile` and reports the image's `PATH` instead of the container's.

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
