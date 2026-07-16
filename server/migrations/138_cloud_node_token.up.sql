-- Cloud node PATs (mcn_) minted by the in-process k8s fleet (kubefleet).
-- Verified locally by auth.LocalCloudPATVerifier instead of the remote
-- Multica Cloud Fleet — same token prefix and middleware path, different
-- authority. One row per node; deleting a node revokes its token rows.
CREATE TABLE cloud_node_token (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    owner_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    node_name TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_cloud_node_token_hash ON cloud_node_token(token_hash);
CREATE INDEX idx_cloud_node_token_workspace_node ON cloud_node_token(workspace_id, node_name);
