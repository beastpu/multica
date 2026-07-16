-- Per-workspace cloud runtime env (LLM proxy keys etc.), configured by
-- workspace admins and injected into kubefleet node pods. Sealed with
-- secretbox under MULTICA_CLOUD_RUNTIME_SECRET_KEY — deliberately a separate
-- table, NOT workspace.settings: that jsonb is shipped verbatim to daemons
-- and must never carry secrets.
CREATE TABLE workspace_cloud_runtime_env (
    workspace_id UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
    env_sealed BYTEA NOT NULL,
    updated_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
