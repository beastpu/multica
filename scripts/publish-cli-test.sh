#!/usr/bin/env bash
# Build and publish the Linux `multica` CLI for a test build, then prune old
# test builds down to the newest few.
#
# Run by the `publish-cli` GitLab job. Kept as a script rather than inline YAML
# so the pruning arithmetic — the part that deletes things — can be run and
# tested outside CI.
#
# Expects, from the environment:
#   IMAGE_TAG               the pipeline's shared tag (`test-YYYYMMDD-<sha>`)
#   OSS_BUCKET              bucket name, no host
#   OSS_ENDPOINT            S3-compatible endpoint
#   AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_DEFAULT_REGION
#
# Set DRY_RUN=1 to print the uploads and deletions without performing them.
set -euo pipefail

: "${IMAGE_TAG:?IMAGE_TAG is required}"
: "${OSS_BUCKET:?OSS_BUCKET is required}"
: "${OSS_ENDPOINT:?OSS_ENDPOINT is required}"

# How many test CLI versions survive a prune. Each version is three objects
# (two tarballs + a checksum manifest), and nothing but a node that has not
# synced yet reads an older one.
KEEP="${CLI_TEST_KEEP:-5}"

DRY_RUN="${DRY_RUN:-0}"
PREFIX="downloads"
# Every artifact this script writes starts here. The release channel publishes
# `multica-cli-<semver>-...`, so the `test-` infix — which IMAGE_TAG always
# carries — is what keeps the two apart in one flat namespace.
TEST_PREFIX="multica-cli-test-"

endpoint="$OSS_ENDPOINT"
case "$endpoint" in
  http://*|https://*) ;;
  *) endpoint="https://$endpoint" ;;
esac

s3() { aws s3 "$@" --endpoint-url "$endpoint"; }
s3api() { aws s3api "$@" --endpoint-url "$endpoint"; }

log() { printf '%s\n' "$*" >&2; }

# Checked before anything is built. A missing tool discovered after two
# cross-compiles wastes the build and reports the wrong thing — the failure
# reads as a checksum problem rather than a runner without the tool.
for tool in go git aws tar; do
  command -v "$tool" >/dev/null || { log "❌ $tool is not on PATH"; exit 1; }
done

# GNU coreutils on the CI runner, BSD on a laptop verifying a change to this
# script. Both print "<digest>  <name>", which is the format `multica update`
# parses, so either satisfies the manifest.
if command -v sha256sum >/dev/null; then
  sha256() { sha256sum "$@"; }
elif command -v shasum >/dev/null; then
  sha256() { shasum -a 256 "$@"; }
else
  log "❌ neither sha256sum nor shasum is available"
  exit 1
fi

# ── build ───────────────────────────────────────────────────────────────
version="$IMAGE_TAG"
commit="$(git rev-parse --short HEAD)"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
ldflags="-X main.version=${version} -X main.commit=${commit} -X main.date=${date}"

rm -rf dist/cli
mkdir -p dist/cli
for arch in amd64 arm64; do
  work="$(mktemp -d)"
  log "building linux/${arch}"
  (cd server && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -ldflags "$ldflags" -o "$work/multica" ./cmd/multica)
  tar -czf "dist/cli/multica-cli-${version}-linux-${arch}.tar.gz" -C "$work" multica
  rm -rf "$work"
done

# Bare names, matching what `multica update` looks up.
(cd dist/cli && sha256 "multica-cli-${version}-linux-"*.tar.gz \
  > "multica-cli-${version}-checksums.txt")

# The test channel's own pointer. `latest-cli.txt` belongs to releases and is
# deliberately not written here: a branch build must never redirect an
# installed client.
printf '%s\n' "$version" > dist/cli/latest-cli-test.txt

ls -la dist/cli

# ── publish ─────────────────────────────────────────────────────────────
# Versioned artifacts go up before the pointer, so a reader that resolves the
# pointer never names a tarball that is not there yet.
upload() {
  local file="$1" cache="$2" name
  name="$(basename "$file")"
  if [ "$DRY_RUN" = "1" ]; then
    log "DRY-RUN upload $name (cache: $cache)"
    return
  fi
  s3 cp "$file" "s3://$OSS_BUCKET/$PREFIX/$name" \
    --cache-control "$cache" --only-show-errors
  log "uploaded $name"
}

for f in dist/cli/multica-cli-"${version}"-*; do
  upload "$f" "max-age=31536000, immutable"
done
upload dist/cli/latest-cli-test.txt "max-age=60"

# ── verify ──────────────────────────────────────────────────────────────
# An upload that silently did not land is the failure mode worth catching
# here: the pointer would name a version no node can fetch.
if [ "$DRY_RUN" != "1" ]; then
  for f in dist/cli/*; do
    name="$(basename "$f")"
    key="$PREFIX/$name"
    found="$(s3api list-objects-v2 --bucket "$OSS_BUCKET" --prefix "$key" \
      --query "Contents[?Key=='$key'].Key | [0]" --output text)"
    [ "$found" = "$key" ] || { log "❌ missing on OSS: s3://$OSS_BUCKET/$key"; exit 1; }
  done
  log "✅ all artifacts verified on OSS"
fi

# ── prune ───────────────────────────────────────────────────────────────
# Versions are sorted by name, which for `test-YYYYMMDD-<sha>` is also
# chronological — the date leads and is fixed-width. Two pipelines on one day
# order by the short sha instead, which is arbitrary but stable; keeping five
# makes that irrelevant.
#
# The version being published is pinned into the keep set, so it survives
# regardless of where its name sorts.
#
# Only objects this script could have written are candidates. The prefix
# already holds hand-published builds from before this job existed —
# `multica-cli-test-20260716-7c13a93d1-kubefleet-...` and friends — which
# carry an extra label segment. A job should garbage-collect what it produces
# and nothing else; somebody may still be pinned to one of those, and they
# would disappear with no commit to point at.
prune() {
  local keys versions keep drop
  keys="$(s3api list-objects-v2 --bucket "$OSS_BUCKET" \
    --prefix "$PREFIX/$TEST_PREFIX" --query 'Contents[].Key' --output text 2>/dev/null || true)"
  [ -n "$keys" ] && [ "$keys" != "None" ] || { log "nothing published yet — no prune"; return; }

  # Exactly the three names a run of this script produces, and nothing that
  # merely starts the same way.
  local ours
  ours="$(printf '%s\n' $keys \
    | grep -E "^$PREFIX/multica-cli-test-[0-9]{8}-[0-9a-f]+-(linux-(amd64|arm64)\.tar\.gz|checksums\.txt)$" \
    || true)"
  [ -n "$ours" ] || { log "no artifacts from this job yet — no prune"; return; }

  versions="$(printf '%s\n' "$ours" \
    | sed -E "s#^$PREFIX/multica-cli-(test-[0-9]{8}-[0-9a-f]+)-.*\$#\\1#" \
    | sort -u)"

  # The version just uploaded is pinned into the keep set rather than sorted
  # into it. Sorting alone is not enough: re-running an older pipeline, or a
  # second pipeline on a day whose short sha happens to sort low, would send
  # this run's own upload to the bottom of the list and delete it.
  local others
  others="$(printf '%s\n' "$versions" | grep -vxF "$version" || true)"
  keep="$(
    printf '%s\n' "$others" | grep . | sort -u | tail -n "$((KEEP - 1))"
    printf '%s\n' "$version"
  )"
  drop="$(comm -23 <(printf '%s\n' "$versions") <(printf '%s\n' "$keep" | sort -u))"

  if [ -z "$drop" ]; then
    log "$(printf '%s\n' "$versions" | wc -l | tr -d ' ') version(s) published, keeping $KEEP — nothing to prune"
    return
  fi

  local v k
  for v in $drop; do
    for k in $(printf '%s\n' "$ours" | grep -F "/multica-cli-${v}-" || true); do
      if [ "$DRY_RUN" = "1" ]; then
        log "DRY-RUN delete $k"
      else
        s3 rm "s3://$OSS_BUCKET/$k" --only-show-errors
        log "pruned $k"
      fi
    done
  done
}

prune

log "published multica CLI $version to $PREFIX/ (keeping newest $KEEP)"
