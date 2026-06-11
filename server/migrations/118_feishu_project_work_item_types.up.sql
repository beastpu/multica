-- Generalize Feishu Project sync from the hardcoded issue type to a
-- configurable list of work-item types. Each entry carries its own status
-- mappings and an optional static project route:
--
--   [{
--     "type_key": "issue",            -- OpenAPI identifier (opaque id for custom types)
--     "api_name": "issue",            -- detail-URL path segment
--     "name": "缺陷",                  -- display label
--     "identifier_prefix": "BUG",     -- [PREFIX-<id>] title prefix
--     "project_id": "",               -- non-empty = static route to this Multica project,
--                                     --   bypassing business-line routing for this type
--     "status_mapping": {...},        -- Feishu status -> Multica status
--     "reverse_status_mapping": {...} -- Multica status -> Feishu status
--   }]
--
-- Backfill: existing integrations were issue-only (sync_issue), so each row
-- gets an issue entry seeded from the legacy flat mapping columns. The legacy
-- columns stay for API compatibility with older desktop clients (the handler
-- dual-writes them from the issue entry).
ALTER TABLE feishu_project_integration
    ADD COLUMN IF NOT EXISTS work_item_types JSONB NOT NULL DEFAULT '[]'::jsonb;

UPDATE feishu_project_integration
SET work_item_types = jsonb_build_array(jsonb_build_object(
        'type_key', 'issue',
        'api_name', 'issue',
        'name', '缺陷',
        'identifier_prefix', 'BUG',
        'project_id', '',
        'status_mapping', status_mapping,
        'reverse_status_mapping', reverse_status_mapping
    ))
WHERE sync_issue = true AND work_item_types = '[]'::jsonb;
