#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${FEISHU_PROJECT_BASE_URL:-https://project.feishu.cn}"
MCP_URL="${FEISHU_PROJECT_MCP_URL:-https://project.feishu.cn/mcp_server/v1}"

required() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "missing required env: $name" >&2
    exit 2
  fi
}

required FEISHU_PROJECT_PLUGIN_ID
required FEISHU_PROJECT_PLUGIN_SECRET
required FEISHU_PROJECT_PROJECT_KEY

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing command: $1" >&2
    exit 2
  fi
}

need curl
need jq

call_tool() {
  local name="$1"
  local args="$2"
  local body
  body="$(jq -nc --arg name "$name" --argjson args "$args" '{
    jsonrpc: "2.0",
    id: now,
    method: "tools/call",
    params: {
      name: $name,
      arguments: $args
    }
  }')"

  local headers=(-H "Content-Type: application/json" -H "Accept: application/json,text/event-stream" -H "X-Mcp-Token: $TOKEN")
  if [[ -n "${FEISHU_PROJECT_ACTOR_USER_KEY:-}" ]]; then
    headers+=(-H "X-USER-KEY: ${FEISHU_PROJECT_ACTOR_USER_KEY}")
  fi

  echo
  echo "== tools/call $name =="
  echo "$args" | jq .
  curl -sS -X POST "$MCP_URL" "${headers[@]}" --data "$body" \
    | jq -r '
      . as $root
      | "isError=" + ((.result.isError // false) | tostring),
        (.result.content[]?.text // empty)
    '
}

token_resp="$(
  jq -nc \
    --arg plugin_id "$FEISHU_PROJECT_PLUGIN_ID" \
    --arg plugin_secret "$FEISHU_PROJECT_PLUGIN_SECRET" \
    '{plugin_id: $plugin_id, plugin_secret: $plugin_secret}' \
  | curl -sS -X POST "$BASE_URL/open_api/authen/plugin_token" \
      -H "Content-Type: application/json" \
      --data-binary @-
)"

echo "== plugin_token response =="
echo "$token_resp" | jq '{
  err_code: (.err_code // .code // null),
  err_msg: (.err_msg // .msg // .message // null),
  has_token: ((.data.token // .data.plugin_token // "") | length > 0)
}'

TOKEN="$(echo "$token_resp" | jq -r '.data.token // .data.plugin_token // empty')"
if [[ -z "$TOKEN" ]]; then
  echo "failed to get plugin token" >&2
  exit 1
fi

project_keys=("$FEISHU_PROJECT_PROJECT_KEY")
if [[ -n "${FEISHU_PROJECT_PROJECT_SIMPLE_NAME:-}" && "${FEISHU_PROJECT_PROJECT_SIMPLE_NAME}" != "${FEISHU_PROJECT_PROJECT_KEY}" ]]; then
  project_keys+=("$FEISHU_PROJECT_PROJECT_SIMPLE_NAME")
fi

for project_key in "${project_keys[@]}"; do
  echo
  echo "######## project_key=$project_key ########"

  call_tool "list_workitem_types" "$(jq -nc --arg project_key "$project_key" '{project_key: $project_key}')"

  for work_item_type in issue "缺陷"; do
    call_tool "list_workitem_field_config" "$(
      jq -nc \
        --arg project_key "$project_key" \
        --arg work_item_type "$work_item_type" \
        '{project_key: $project_key, work_item_type: $work_item_type, page_num: 1}'
    )"
  done

  call_tool "search_by_mql" "$(
    jq -nc \
      --arg project_key "$project_key" \
      --arg mql "SELECT \`work_item_id\`, \`name\`, \`work_item_status\` FROM \`${project_key}\`.\`issue\` LIMIT 3" \
      '{project_key: $project_key, mql: $mql}'
  )"

  if [[ -n "${FEISHU_PROJECT_WORK_ITEM_ID:-}" ]]; then
    call_tool "get_workitem_brief" "$(
      jq -nc \
        --arg project_key "$project_key" \
        --arg work_item_id "$FEISHU_PROJECT_WORK_ITEM_ID" \
        '{project_key: $project_key, work_item_id: $work_item_id, fields: ["work_item_id", "name", "work_item_status"]}'
    )"
  fi
done
