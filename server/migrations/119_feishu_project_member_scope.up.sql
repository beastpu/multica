-- Per-workspace assignee scope for Feishu Project sync. When a single Meego
-- space is shared by several teams that each run their own Multica workspace
-- (e.g. 服务端 / 客户端 / 策划 onboard at different paces), each workspace can
-- restrict sync to the work items whose 处理人 (operator) is a member of THAT
-- workspace — so a team only sees its own tickets.
--
--   false (default) — sync every item the type/status/routing allows (current
--                     behavior; for a team that owns the whole space).
--   true            — only sync an item when its operator email resolves to a
--                     member of this workspace. The gate is applied at issue
--                     CREATION only; already-synced issues keep syncing even if
--                     the operator is later reassigned away.
ALTER TABLE feishu_project_integration
    ADD COLUMN IF NOT EXISTS sync_only_workspace_member_items BOOLEAN NOT NULL DEFAULT false;
