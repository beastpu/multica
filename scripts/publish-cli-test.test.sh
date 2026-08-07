#!/usr/bin/env bash
# Tests the pruning arithmetic in publish-cli-test.sh against a fake `aws`.
#
# Pruning is the only part of that script that deletes anything, and it runs
# unattended against the same bucket a released client reads from. The two
# properties worth holding it to are that it never drops the version it just
# published, and that it never touches the release channel — neither is
# observable until the day it is wrong.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fake_bin="$(mktemp -d)"
trap 'rm -rf "$fake_bin"' EXIT

# Fake `aws`: list-objects-v2 replays $FAKE_KEYS, everything else is a no-op
# that records the call. `s3 rm` is what the assertions read back.
cat > "$fake_bin/aws" <<'FAKE'
#!/usr/bin/env bash
case "$1 $2" in
  "s3api list-objects-v2")
    for k in $FAKE_KEYS; do printf '%s\t' "$k"; done; printf '\n'
    ;;
  "s3 rm")
    printf '%s\n' "$3" >> "$DELETED_LOG"
    ;;
  *) : ;;
esac
FAKE
chmod +x "$fake_bin/aws"

keys_for() {
  local v
  for v in "$@"; do
    printf 'downloads/multica-cli-%s-linux-amd64.tar.gz ' "$v"
    printf 'downloads/multica-cli-%s-linux-arm64.tar.gz ' "$v"
    printf 'downloads/multica-cli-%s-checksums.txt ' "$v"
  done
}

run_prune() {
  local publishing="$1"; shift
  DELETED_LOG="$(mktemp)"
  export DELETED_LOG
  FAKE_KEYS="$(keys_for "$@")" \
  PATH="$fake_bin:$PATH" \
  IMAGE_TAG="$publishing" OSS_BUCKET=b OSS_ENDPOINT=https://e \
  CLI_TEST_KEEP=5 DRY_RUN=0 \
  bash -c '
    set -euo pipefail
    # Source only the prune half: the build half needs a Go toolchain and a
    # repo, and neither is what is under test here.
    sed -n "/^# ── prune ─/,/^}$/p" "'"$here"'/publish-cli-test.sh" > /tmp/prune-only.sh
    version="$IMAGE_TAG"; KEEP="$CLI_TEST_KEEP"; PREFIX=downloads
    TEST_PREFIX="multica-cli-test-"; DRY_RUN=0
    endpoint="$OSS_ENDPOINT"
    s3() { aws s3 "$@"; }; s3api() { aws s3api "$@"; }
    log() { :; }
    source /tmp/prune-only.sh
    prune
  '
  cat "$DELETED_LOG" 2>/dev/null || true
}

fail() { printf '❌ %s\n' "$1" >&2; exit 1; }

# Six already up plus the one being published is seven; keeping five drops the
# two oldest. The assertion is on what survives, because "keep the newest 5" is
# the rule — how many objects that costs is arithmetic.
deleted="$(run_prune test-20260807-aaaaaaaa \
  test-20260801-11111111 test-20260802-22222222 test-20260803-33333333 \
  test-20260804-44444444 test-20260805-55555555 test-20260806-66666666)"
dropped_versions="$(printf '%s\n' "$deleted" | sed -E 's#.*/multica-cli-(test-[0-9]{8}-[0-9a-f]+)-.*#\1#' | sort -u | grep . || true)"
[ "$(printf '%s\n' "$dropped_versions" | grep -c .)" = "2" ] \
  || fail "expected the 2 oldest versions dropped, got: $dropped_versions"
printf '%s\n' "$dropped_versions" | grep -q "test-20260801-11111111" \
  || fail "oldest version survived: $dropped_versions"
printf '%s\n' "$dropped_versions" | grep -q "test-20260806-66666666" \
  && fail "pruned a version that should have been kept: $dropped_versions"

# The version being published survives even when the listing already has five
# newer-sorting names — otherwise a pipeline would delete its own upload.
deleted="$(run_prune test-20260801-00000000 \
  test-20260801-00000000 test-20260802-22222222 test-20260803-33333333 \
  test-20260804-44444444 test-20260805-55555555 test-20260806-66666666)"
printf '%s\n' "$deleted" | grep -q "test-20260801-00000000" \
  && fail "pruned the version it was publishing: $deleted"

# Release artifacts share the prefix and must be invisible to the prune.
deleted="$(run_prune test-20260807-aaaaaaaa \
  test-20260801-11111111 test-20260802-22222222 test-20260803-33333333 \
  test-20260804-44444444 test-20260805-55555555 test-20260806-66666666)"
printf '%s\n' "$deleted" | grep -qE "multica-cli-[0-9]+\.[0-9]+" \
  && fail "prune touched a release artifact: $deleted"

# Builds published by hand before this job existed carry an extra label
# segment. They are not this job's to delete: someone may still be pinned to
# one, and it would vanish with no commit to point at.
DELETED_LOG="$(mktemp)"; export DELETED_LOG
legacy="downloads/multica-cli-test-20260716-7c13a93d1-kubefleet-linux-amd64.tar.gz downloads/multica-cli-test-20260709-ddc01a9-develop-checksums.txt"
deleted="$(FAKE_KEYS="$legacy $(keys_for \
  test-20260801-11111111 test-20260802-22222222 test-20260803-33333333 \
  test-20260804-44444444 test-20260805-55555555 test-20260806-66666666)" \
  PATH="$fake_bin:$PATH" \
  IMAGE_TAG=test-20260807-aaaaaaaa OSS_BUCKET=b OSS_ENDPOINT=https://e \
  CLI_TEST_KEEP=5 DRY_RUN=0 \
  bash -c '
    set -euo pipefail
    sed -n "/^# ── prune ─/,/^}$/p" "'"$here"'/publish-cli-test.sh" > /tmp/prune-only.sh
    version="$IMAGE_TAG"; KEEP="$CLI_TEST_KEEP"; PREFIX=downloads
    TEST_PREFIX="multica-cli-test-"; DRY_RUN=0
    endpoint="$OSS_ENDPOINT"
    s3() { aws s3 "$@"; }; s3api() { aws s3api "$@"; }
    log() { :; }
    source /tmp/prune-only.sh
    prune
  '; cat "$DELETED_LOG" 2>/dev/null || true)"
printf '%s\n' "$deleted" | grep -qE "kubefleet|develop" \
  && fail "pruned a hand-published build: $deleted"
printf '%s\n' "$deleted" | grep -q "test-20260801-11111111" \
  || fail "own oldest build survived when it should have been pruned: $deleted"

# Five or fewer published → nothing to do.
deleted="$(run_prune test-20260807-aaaaaaaa \
  test-20260803-33333333 test-20260804-44444444 test-20260805-55555555)"
[ -z "$(printf '%s' "$deleted" | tr -d '[:space:]')" ] \
  || fail "pruned below the keep threshold: $deleted"

printf '✅ publish-cli-test.sh prune: 5 cases passed\n'
