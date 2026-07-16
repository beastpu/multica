-- name: CreateCloudNodeToken :one
INSERT INTO cloud_node_token (token_hash, workspace_id, owner_id, node_name, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetCloudNodeTokenByHash :one
SELECT * FROM cloud_node_token
WHERE token_hash = $1 AND expires_at > now();

-- name: DeleteCloudNodeTokensByNode :exec
-- Revokes every token minted for a node. Called when the node's Deployment
-- is deleted so a leaked token dies with the pod.
DELETE FROM cloud_node_token
WHERE workspace_id = $1 AND node_name = $2;
