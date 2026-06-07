#!/usr/bin/env bash
# create-workspace.sh — provision a Multica workspace + shareable invite link.
#
# Usage:
#   ./create-workspace.sh [-e test|prod] [-r member|admin] [-i inviter_email] \
#       <workspace_name> [note_email]
#
# The script connects to the target environment's PostgreSQL via the
# `psql-debug` pod in the `multica` namespace (which has psql + network reach
# to both RDS instances) and runs one atomic CTE that:
#   1. looks up the inviter user by email,
#   2. INSERTs the workspace,
#   3. adds the inviter as owner in `member`,
#   4. creates a shareable workspace_invitation (invitee_email NULL,
#      max_uses NULL = unlimited).
# It then prints `<APP_URL>/invite/<id>` for sharing.
#
# `note_email` is optional and only echoed in the log line — it does NOT bind
# the invitation. The link is shareable regardless.

set -euo pipefail

ENV_TARGET="test"
ROLE="member"
INVITER_EMAIL="beastpu@lilith.com"

usage() {
  cat <<EOF
Usage: $0 [-e test|prod] [-r member|admin] [-i inviter_email] <workspace_name> [note_email]

Options:
  -e  Target environment: 'test' (multica-test ns) or 'prod' (multica ns). Default: test.
  -r  Invite role: 'member' or 'admin'. Default: member.
  -i  Inviter email (must exist as a user in the target DB). Default: beastpu@lilith.com.

Examples:
  $0 "My Team"
  $0 -e test -r admin "QA Sandbox" zhangsan@lilith.com
  $0 -e prod "Growth Squad"
EOF
}

while getopts ":e:r:i:h" opt; do
  case "$opt" in
    e) ENV_TARGET="$OPTARG" ;;
    r) ROLE="$OPTARG" ;;
    i) INVITER_EMAIL="$OPTARG" ;;
    h) usage; exit 0 ;;
    \?) echo "unknown option: -$OPTARG" >&2; usage; exit 2 ;;
    :)  echo "option -$OPTARG needs a value" >&2; usage; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

if [ $# -lt 1 ]; then usage; exit 2; fi
WORKSPACE_NAME="$1"
NOTE_EMAIL="${2:-}"

case "$ENV_TARGET" in
  test) NS="multica-test"; APP_URL="https://multica-test.lilithgames.com" ;;
  prod) NS="multica";      APP_URL="https://multica.lilithgames.com" ;;
  *) echo "invalid env: $ENV_TARGET (use test or prod)" >&2; exit 2 ;;
esac
case "$ROLE" in
  member|admin) ;;
  *) echo "invalid role: $ROLE (use member or admin)" >&2; exit 2 ;;
esac

# Derive slug: lowercase, [a-z0-9]+ → '-', strip leading/trailing '-'. Always
# append a 4-hex suffix so we never collide with reserved slugs or existing
# workspaces — reserved-slug list lives in Go and is not worth duplicating here.
ascii_slug=$(printf '%s' "$WORKSPACE_NAME" \
  | tr '[:upper:]' '[:lower:]' \
  | LC_ALL=C sed -E 's/[^a-z0-9]+/-/g; s/^-+|-+$//g')
[ -z "$ascii_slug" ] && ascii_slug="ws"
suffix=$(openssl rand -hex 2)
SLUG="${ascii_slug}-${suffix}"

echo "▶ env=$ENV_TARGET ns=$NS"
echo "  inviter=$INVITER_EMAIL"
echo "  workspace=\"$WORKSPACE_NAME\" slug=$SLUG role=$ROLE"
[ -n "$NOTE_EMAIL" ] && echo "  note: link is for $NOTE_EMAIL (not bound)"

# Pull the target env's DATABASE_URL from its k8s secret (we exec psql in the
# multica namespace's psql-debug pod regardless, and pass the URL explicitly).
DATABASE_URL=$(kubectl -n "$NS" get secret multica-secrets -o jsonpath='{.data.DATABASE_URL}' | base64 -d)
if [ -z "$DATABASE_URL" ]; then
  echo "failed to read DATABASE_URL from $NS/multica-secrets" >&2
  exit 1
fi

SQL=$(cat <<'EOSQL'
WITH inviter AS (
  SELECT id FROM "user" WHERE email = :'inviter_email'
),
ws AS (
  INSERT INTO workspace (name, slug) VALUES (:'ws_name', :'ws_slug') RETURNING id
),
membership AS (
  INSERT INTO member (workspace_id, user_id, role)
  SELECT ws.id, inviter.id, 'owner' FROM ws, inviter
  RETURNING workspace_id
)
INSERT INTO workspace_invitation (workspace_id, inviter_id, invitee_email, role, max_uses)
SELECT ws.id, inviter.id, NULL, :'role', NULL FROM ws, inviter
RETURNING id;
EOSQL
)

# `psql -1` wraps everything in a single transaction; ON_ERROR_STOP aborts the
# tx on the first failure (e.g. unknown inviter → empty CTE → INSERT inserts 0
# rows, which we detect via empty stdout and surface as an error).
INVITATION_ID=$(printf '%s\n' "$SQL" | kubectl -n multica exec -i deploy/psql-debug -- \
  psql -A -t -X -q -1 -v ON_ERROR_STOP=1 \
    -v "inviter_email=$INVITER_EMAIL" \
    -v "ws_name=$WORKSPACE_NAME" \
    -v "ws_slug=$SLUG" \
    -v "role=$ROLE" \
    "$DATABASE_URL" -f - \
  | grep -Eo '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' \
  | head -1)

if [ -z "$INVITATION_ID" ]; then
  echo "✗ no invitation row returned — inviter '$INVITER_EMAIL' likely missing in $ENV_TARGET DB" >&2
  exit 1
fi

INVITE_URL="$APP_URL/invite/$INVITATION_ID"

echo
echo "✓ workspace ready"
echo
echo "──── 发给用户 ────"
echo "加入工作区："
echo "$INVITE_URL"
echo
echo "注册运行时（把下面这句发给 Claude）："
echo "阅读 https://multica.lilithgames.com/doc/install.md，按照这个执行安装或更新"
echo "──────────────────"

# If a note_email was provided, also send the invite via Feishu DM through the
# multica-feishu-bridge app (`cli_aa8861f3c0bb9cde`). Scopes confirmed in-session:
# im:message:send_as_bot + contact:user.email→id. Failures are surfaced but
# don't roll back the workspace.
if [ -n "$NOTE_EMAIL" ]; then
  echo
  echo "▶ 飞书 DM → $NOTE_EMAIL"
  F_APP_ID=$(kubectl -n multica get secret multica-feishu-bridge-secrets -o jsonpath='{.data.FEISHU_APP_ID}' | base64 -d)
  F_APP_SECRET=$(kubectl -n multica get secret multica-feishu-bridge-secrets -o jsonpath='{.data.FEISHU_APP_SECRET}' | base64 -d)
  F_TOKEN=$(curl -sS -X POST https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal \
    -H "Content-Type: application/json" \
    -d "{\"app_id\":\"$F_APP_ID\",\"app_secret\":\"$F_APP_SECRET\"}" \
    | python3 -c "import json,sys; print(json.load(sys.stdin).get('tenant_access_token',''))")
  if [ -z "$F_TOKEN" ]; then
    echo "✗ 拿不到 tenant_access_token，未发送" >&2
  else
    PAYLOAD=$(python3 - "$INVITE_URL" "$NOTE_EMAIL" <<'PY'
import json, sys
url, email = sys.argv[1], sys.argv[2]
text = (
    "加入工作区：\n"
    f"{url}\n\n"
    "注册运行时（把下面这句发给 Claude）：\n"
    "阅读 https://multica.lilithgames.com/doc/install.md，按照这个执行安装或更新"
)
print(json.dumps({
    "receive_id": email,
    "msg_type": "text",
    "content": json.dumps({"text": text}, ensure_ascii=False),
}, ensure_ascii=False))
PY
)
    RESP=$(curl -sS -X POST "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=email" \
      -H "Authorization: Bearer $F_TOKEN" \
      -H "Content-Type: application/json; charset=utf-8" \
      --data-binary "$PAYLOAD")
    CODE=$(printf '%s' "$RESP" | python3 -c "import json,sys; print(json.load(sys.stdin).get('code','?'))")
    if [ "$CODE" = "0" ]; then
      echo "✓ 已发送"
    else
      MSG=$(printf '%s' "$RESP" | python3 -c "import json,sys; print(json.load(sys.stdin).get('msg','unknown'))")
      echo "✗ 飞书发送失败: code=$CODE msg=$MSG" >&2
    fi
  fi
fi
