package cloudruntime

import "context"

// Provider is a pluggable cloud-runtime node backend. The Fleet adapter (see
// fleet.go) owns everything provider-agnostic — HTTP dispatch, workspace auth,
// the node quota check, minting/revoking the node's mcn_ PAT, and cascading
// runtime-row cleanup — and calls a Provider only for the node lifecycle
// itself. This is the seam for supporting more than one backend: kubefleet
// (Kubernetes) today, an ECS provider later, behind the same interface.
//
// A Provider receives the already-minted node token and the already-decrypted
// workspace env in NodeSpec and is responsible only for delivering them to the
// machine it provisions (a k8s Secret + StatefulSet, an ECS instance +
// user-data, …). It never touches the database.
type Provider interface {
	// Name identifies the backend (e.g. "k8s") for logs/telemetry.
	Name() string
	// CreateNode provisions one node from spec and returns its initial view.
	CreateNode(ctx context.Context, spec NodeSpec) (Node, error)
	// ListNodes returns the workspace's current nodes.
	ListNodes(ctx context.Context, workspaceID, workspaceSlug string) ([]Node, error)
	// DeleteNode tears a node down (idempotent — a missing node is not an error).
	DeleteNode(ctx context.Context, workspaceID, workspaceSlug, nodeName string) error
	// CountNodes returns the workspace's current node count for the quota check.
	CountNodes(ctx context.Context, workspaceID, workspaceSlug string) (int, error)
}

// NodeSpec is the fully-resolved request to provision one node. The Fleet
// adapter fills it — including the minted Token and the decrypted workspace
// Env — before handing it to the Provider.
type NodeSpec struct {
	WorkspaceID   string
	WorkspaceSlug string
	Name          string // stable node id, e.g. "node-a1b2c3d4"
	DisplayName   string // user-facing name
	OwnerID       string // creator user id
	InstanceType  string
	DiskSizeGB    int
	// Token is the node's mcn_ PAT, to be delivered into the machine so its
	// daemon can authenticate.
	Token string
	// Env is the workspace-level runtime env (LLM proxy base URLs, models,
	// keys), decrypted by the adapter, to be delivered into the machine.
	Env map[string]string
}

// Node is a backend-agnostic view of a provisioned node, shaped to the
// CloudRuntimeNode the frontend already consumes.
type Node struct {
	ID           string // stable node id (node-xxxx); the "id" and "instance_id"
	DisplayName  string // user-facing name; the "name"
	Status       string
	InstanceType string
	ImageID      string
	Region       string
	SubnetID     string
	OwnerID      string
	CreatedAt    string
	UpdatedAt    string
}
