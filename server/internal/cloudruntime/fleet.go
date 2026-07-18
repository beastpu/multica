package cloudruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	defaultNodeTokenTTL         = 180 * 24 * time.Hour
	defaultMaxNodesPerWorkspace = 3
	defaultDiskSizeGB           = 20
	minDiskSizeGB               = 20
	maxDiskSizeGB               = 100
	diskSizeStepGB              = 10
)

// Fleet is the provider-agnostic cloud-runtime adapter. It implements the
// cloudRuntimeProxy Do() contract the handler layer speaks, handling
// everything that does not depend on the backend — workspace auth, the node
// quota, minting/revoking the node's mcn_ PAT, decrypting the workspace env,
// and cascading runtime-row cleanup — and delegates the node lifecycle to a
// Provider (kubefleet today, ECS later).
type Fleet struct {
	provider    Provider
	queries     *db.Queries
	envBox      *secretbox.Box
	tokenTTL    time.Duration
	maxPerWS    int
	displayName string
}

// FleetConfig assembles a Fleet.
type FleetConfig struct {
	Provider Provider
	Queries  *db.Queries
	// EnvBox decrypts workspace_cloud_runtime_env. Nil disables workspace env
	// delivery (nodes get no LLM config).
	EnvBox *secretbox.Box
	// NodeTokenTTL bounds a minted mcn_ PAT (default 180 days).
	NodeTokenTTL time.Duration
	// MaxNodesPerWorkspace caps nodes per workspace (default 3).
	MaxNodesPerWorkspace int
}

// NewFleet returns a Fleet, or nil when no provider is configured (so the
// handler's nil check disables the cloud-runtime routes).
func NewFleet(cfg FleetConfig) *Fleet {
	if cfg.Provider == nil || cfg.Queries == nil {
		return nil
	}
	if cfg.NodeTokenTTL <= 0 {
		cfg.NodeTokenTTL = defaultNodeTokenTTL
	}
	if cfg.MaxNodesPerWorkspace <= 0 {
		cfg.MaxNodesPerWorkspace = defaultMaxNodesPerWorkspace
	}
	return &Fleet{
		provider: cfg.Provider,
		queries:  cfg.Queries,
		envBox:   cfg.EnvBox,
		tokenTTL: cfg.NodeTokenTTL,
		maxPerWS: cfg.MaxNodesPerWorkspace,
	}
}

func (f *Fleet) Enabled() bool { return f != nil }

// Do dispatches the fleet REST surface the handler proxies to, mirroring the
// remote Fleet contract so handler/cloud_runtime.go needs no changes.
func (f *Fleet) Do(ctx context.Context, req Request) (*Response, error) {
	if f == nil {
		return nil, ErrDisabled
	}
	switch {
	case req.Path == "/healthz" || req.Path == "/readyz":
		return jsonResponse(http.StatusOK, map[string]string{"status": "ok"})
	case req.Path == "/api/v1/" && req.Method == http.MethodGet:
		return jsonResponse(http.StatusOK, map[string]string{"service": f.provider.Name(), "status": "ok"})
	case req.Path == "/api/v1/nodes":
		switch req.Method {
		case http.MethodGet:
			return f.listNodes(ctx)
		case http.MethodPost:
			return f.createNode(ctx, req)
		case http.MethodDelete:
			return f.deleteNode(ctx, req)
		}
	case req.Path == "/api/v1/nodes/reboot" && req.Method == http.MethodPost:
		return f.restartNode(ctx, req)
	case req.Path == "/api/v1/workspace-env" && req.Method == http.MethodPut:
		return f.syncWorkspaceEnv(ctx, req)
	}
	return jsonResponse(http.StatusNotImplemented, map[string]string{"error": "operation not supported"})
}

// scope resolves the request's workspace id, slug and member from the
// middleware-injected context. The /api/cloud-runtime routes sit inside the
// RequireWorkspaceMember group, so all are present for well-formed requests.
func (f *Fleet) scope(ctx context.Context) (wsID, slug string, member db.Member, resp *Response) {
	wsID = middleware.WorkspaceIDFromContext(ctx)
	m, ok := middleware.MemberFromContext(ctx)
	if wsID == "" || !ok {
		r, _ := jsonResponse(http.StatusBadRequest, map[string]string{"error": "workspace context is required"})
		return "", "", db.Member{}, r
	}
	if wsUUID, err := util.ParseUUID(wsID); err == nil {
		if ws, werr := f.queries.GetWorkspace(ctx, wsUUID); werr == nil {
			slug = ws.Slug
		}
	}
	return wsID, slug, m, nil
}

func isWorkspaceAdmin(m db.Member) bool { return m.Role == "owner" || m.Role == "admin" }

type createNodeRequest struct {
	Name         string `json:"name"`
	InstanceType string `json:"instance_type"`
	DiskSizeGB   int    `json:"disk_size_gb"`
}

func (f *Fleet) createNode(ctx context.Context, req Request) (*Response, error) {
	wsID, slug, member, errResp := f.scope(ctx)
	if errResp != nil {
		return errResp, nil
	}
	if !isWorkspaceAdmin(member) {
		return jsonResponse(http.StatusForbidden, map[string]string{
			"error": "only workspace owners and admins can create cloud runtime nodes",
		})
	}
	var body createNodeRequest
	if len(req.Body) > 0 {
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
	}
	wsUUID, err := util.ParseUUID(wsID)
	if err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}

	diskSizeGB := body.DiskSizeGB
	if diskSizeGB <= 0 {
		diskSizeGB = defaultDiskSizeGB
	}
	if diskSizeGB < minDiskSizeGB || diskSizeGB > maxDiskSizeGB {
		return jsonResponse(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("disk_size_gb must be between %d and %d", minDiskSizeGB, maxDiskSizeGB),
		})
	}
	if diskSizeGB%diskSizeStepGB != 0 {
		return jsonResponse(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("disk_size_gb must use %d GiB increments", diskSizeStepGB),
		})
	}

	// Friendly quota check; the provider is expected to enforce a race-proof
	// backstop of its own (e.g. a namespace ResourceQuota).
	if n, cerr := f.provider.CountNodes(ctx, wsID, slug); cerr != nil {
		return nil, cerr
	} else if n >= f.maxPerWS {
		return jsonResponse(http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("workspace node limit reached (%d)", f.maxPerWS),
		})
	}

	nodeName, err := randomNodeName()
	if err != nil {
		return nil, err
	}
	displayName := trimOr(body.Name, nodeName)

	// Mint the node PAT before provisioning so a half-created node never runs
	// without a revocable credential; roll the row back if provisioning fails.
	token, err := auth.GenerateCloudNodeToken()
	if err != nil {
		return nil, err
	}
	if _, err := f.queries.CreateCloudNodeToken(ctx, db.CreateCloudNodeTokenParams{
		TokenHash:   auth.HashToken(token),
		WorkspaceID: wsUUID,
		OwnerID:     member.UserID,
		NodeName:    nodeName,
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(f.tokenTTL), Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("create cloud node token: %w", err)
	}

	env, err := f.workspaceEnv(ctx, wsUUID)
	if err != nil {
		f.revokeToken(ctx, wsUUID, nodeName)
		return nil, err
	}

	node, err := f.provider.CreateNode(ctx, NodeSpec{
		WorkspaceID:   wsID,
		WorkspaceSlug: slug,
		Name:          nodeName,
		DisplayName:   displayName,
		OwnerID:       util.UUIDToString(member.UserID),
		InstanceType:  body.InstanceType,
		DiskSizeGB:    diskSizeGB,
		Token:         token,
		Env:           env,
	})
	if err != nil {
		f.revokeToken(ctx, wsUUID, nodeName)
		return nil, err
	}
	return jsonResponse(http.StatusCreated, nodeJSON(node))
}

func (f *Fleet) listNodes(ctx context.Context) (*Response, error) {
	wsID, slug, _, errResp := f.scope(ctx)
	if errResp != nil {
		return errResp, nil
	}
	nodes, err := f.provider.ListNodes(ctx, wsID, slug)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeJSON(n))
	}
	return jsonResponse(http.StatusOK, out)
}

type deleteNodeRequest struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
}

func (f *Fleet) deleteNode(ctx context.Context, req Request) (*Response, error) {
	wsID, slug, member, errResp := f.scope(ctx)
	if errResp != nil {
		return errResp, nil
	}
	if !isWorkspaceAdmin(member) {
		return jsonResponse(http.StatusForbidden, map[string]string{
			"error": "only workspace owners and admins can delete cloud runtime nodes",
		})
	}
	var body deleteNodeRequest
	if len(req.Body) > 0 {
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
	}
	nodeName := trimOr(body.InstanceID, body.ID)
	// Node ids are generated as "node-<hex>"; rejecting anything else keeps
	// arbitrary backend object names out of reach.
	if len(nodeName) < 5 || nodeName[:5] != "node-" {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "instance_id is required"})
	}

	if err := f.provider.DeleteNode(ctx, wsID, slug, nodeName); err != nil {
		return nil, err
	}
	if wsUUID, err := util.ParseUUID(wsID); err == nil {
		f.revokeToken(ctx, wsUUID, nodeName)
		// Cascade: drop the offline cloud runtime rows this node registered
		// (daemon_id = node name) so they don't linger as UI orphans. Rows
		// with an agent still bound are skipped by the query.
		if _, derr := f.queries.DeleteCloudRuntimesByNode(ctx, db.DeleteCloudRuntimesByNodeParams{
			WorkspaceID: wsUUID,
			DaemonID:    pgtype.Text{String: nodeName, Valid: true},
		}); derr != nil {
			return nil, fmt.Errorf("delete cloud runtimes for node: %w", derr)
		}
	}
	return jsonResponse(http.StatusOK, map[string]string{"status": "deleted", "id": nodeName})
}

func (f *Fleet) restartNode(ctx context.Context, req Request) (*Response, error) {
	wsID, slug, member, errResp := f.scope(ctx)
	if errResp != nil {
		return errResp, nil
	}
	if !isWorkspaceAdmin(member) {
		return jsonResponse(http.StatusForbidden, map[string]string{
			"error": "only workspace owners and admins can restart cloud runtime nodes",
		})
	}
	var body deleteNodeRequest
	if len(req.Body) > 0 {
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
	}
	nodeName := trimOr(body.InstanceID, body.ID)
	if len(nodeName) < 5 || nodeName[:5] != "node-" {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "instance_id is required"})
	}
	if err := f.provider.RestartNode(ctx, wsID, slug, nodeName); err != nil {
		return nil, err
	}
	return jsonResponse(http.StatusOK, map[string]string{"status": "rebooting", "id": nodeName})
}

type syncWorkspaceEnvRequest struct {
	Env map[string]string `json:"env"`
}

func (f *Fleet) syncWorkspaceEnv(ctx context.Context, req Request) (*Response, error) {
	wsID, slug, member, errResp := f.scope(ctx)
	if errResp != nil {
		return errResp, nil
	}
	if !isWorkspaceAdmin(member) {
		return jsonResponse(http.StatusForbidden, map[string]string{
			"error": "only workspace owners and admins can sync cloud runtime env",
		})
	}
	var body syncWorkspaceEnvRequest
	if len(req.Body) > 0 {
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
	}
	if err := f.provider.SyncWorkspaceEnv(ctx, wsID, slug, body.Env); err != nil {
		return nil, err
	}
	return jsonResponse(http.StatusOK, map[string]string{"status": "synced"})
}

// workspaceEnv decrypts the workspace's cloud runtime env, or returns nil when
// none is configured / no EnvBox is wired.
func (f *Fleet) workspaceEnv(ctx context.Context, wsUUID pgtype.UUID) (map[string]string, error) {
	if f.envBox == nil {
		return nil, nil
	}
	row, err := f.queries.GetWorkspaceCloudRuntimeEnv(ctx, wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load workspace env: %w", err)
	}
	plaintext, err := f.envBox.Open(row.EnvSealed)
	if err != nil {
		return nil, fmt.Errorf("open workspace env: %w", err)
	}
	var env map[string]string
	if err := json.Unmarshal(plaintext, &env); err != nil {
		return nil, fmt.Errorf("decode workspace env: %w", err)
	}
	return env, nil
}

func (f *Fleet) revokeToken(ctx context.Context, wsUUID pgtype.UUID, nodeName string) {
	_ = f.queries.DeleteCloudNodeTokensByNode(context.WithoutCancel(ctx), db.DeleteCloudNodeTokensByNodeParams{
		WorkspaceID: wsUUID,
		NodeName:    nodeName,
	})
}

// nodeJSON shapes a Node into the CloudRuntimeNode the frontend consumes.
func nodeJSON(n Node) map[string]any {
	name := n.DisplayName
	if name == "" {
		name = n.ID
	}
	return map[string]any{
		"id":            n.ID,
		"owner_id":      n.OwnerID,
		"instance_id":   n.ID,
		"region":        n.Region,
		"instance_type": n.InstanceType,
		"image_id":      n.ImageID,
		"subnet_id":     n.SubnetID,
		"name":          name,
		"status":        n.Status,
		"tags":          map[string]string{},
		"metadata":      map[string]any{},
		"created_at":    n.CreatedAt,
		"updated_at":    n.UpdatedAt,
	}
}

func randomNodeName() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "node-" + hex.EncodeToString(b), nil
}

func trimOr(primary, fallback string) string {
	if s := strings.TrimSpace(primary); s != "" {
		return s
	}
	return strings.TrimSpace(fallback)
}

func jsonResponse(status int, v any) (*Response, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return &Response{StatusCode: status, Header: h, Body: raw}, nil
}
