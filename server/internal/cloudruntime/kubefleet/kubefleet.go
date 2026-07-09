// Package kubefleet is an in-process implementation of the cloud runtime
// fleet for self-hosted Kubernetes deployments. It satisfies the same
// interface the remote Multica Cloud Fleet proxy client does
// (handler.cloudRuntimeProxy), so the /api/cloud-runtime/* HTTP surface and
// the frontend stay untouched — only the node authority changes.
//
// Model: one Kubernetes namespace per workspace, one single-replica
// Deployment per node running the multica daemon. Each node gets a locally
// minted mcn_ PAT (cloud_node_token table) injected via a Secret; the daemon
// inside the pod authenticates with it, registers with runtime_mode=cloud,
// and is pinned to its workspace via MULTICA_WATCH_WORKSPACE_IDS.
//
// v1 implements exactly what the CloudRuntimeDialog uses: list, create,
// delete. start/stop/reboot/exec return 501.
package kubefleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	managedByLabel = "app.kubernetes.io/managed-by"
	managedByValue = "multica-kubefleet"
	workspaceLabel = "multica.io/workspace-id"
	nodeLabel      = "multica.io/node"

	annotationDisplayName  = "multica.io/display-name"
	annotationOwnerID      = "multica.io/owner-id"
	annotationInstanceType = "multica.io/instance-type"

	defaultKubeAPIURL      = "https://kubernetes.default.svc"
	defaultTokenFile       = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	defaultCAFile          = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	defaultNamespacePrefix = "mrt-"
	defaultNodeTokenTTL    = 180 * 24 * time.Hour
)

// instanceResources maps the instance_type strings the dialog offers onto pod
// resource requests/limits. Unknown types fall back to the smallest preset —
// degrade, don't reject, so a newer frontend can't brick node creation.
var instanceResources = map[string]podResources{
	"t4g.medium": {CPU: "1", Memory: "2Gi"},
	"t4g.large":  {CPU: "2", Memory: "4Gi"},
}

var defaultResources = podResources{CPU: "1", Memory: "2Gi"}

type podResources struct {
	CPU    string
	Memory string
}

type Config struct {
	// Image is the container image every node pod runs. Must contain the
	// multica CLI plus at least one agent CLI. Required.
	Image string
	// ServerURL is the multica server base URL as reachable FROM the pods
	// (e.g. the in-cluster service URL). Required.
	ServerURL string
	// KubeAPIURL, TokenFile and CAFile locate the Kubernetes API. Defaults
	// are the standard in-cluster paths; tests point them elsewhere.
	KubeAPIURL string
	TokenFile  string
	CAFile     string
	// NamespacePrefix prefixes the per-workspace namespace name
	// (default "mrt-"; namespace = prefix + workspace UUID without dashes).
	NamespacePrefix string
	// NodeTokenTTL bounds the minted mcn_ PAT lifetime (default 180 days).
	NodeTokenTTL time.Duration
	// HTTPClient overrides the Kubernetes API client (tests). When nil a
	// client is built from CAFile.
	HTTPClient *http.Client
}

type Fleet struct {
	cfg     Config
	queries *db.Queries
	http    *http.Client
}

// New validates cfg, applies defaults and returns a ready Fleet.
func New(cfg Config, queries *db.Queries) (*Fleet, error) {
	if strings.TrimSpace(cfg.Image) == "" {
		return nil, fmt.Errorf("kubefleet: image is required (MULTICA_CLOUD_RUNTIME_IMAGE)")
	}
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, fmt.Errorf("kubefleet: server URL is required (MULTICA_CLOUD_RUNTIME_SERVER_URL)")
	}
	if queries == nil {
		return nil, fmt.Errorf("kubefleet: queries is required")
	}
	if cfg.KubeAPIURL == "" {
		cfg.KubeAPIURL = defaultKubeAPIURL
	}
	cfg.KubeAPIURL = strings.TrimRight(cfg.KubeAPIURL, "/")
	if cfg.TokenFile == "" {
		cfg.TokenFile = defaultTokenFile
	}
	if cfg.CAFile == "" {
		cfg.CAFile = defaultCAFile
	}
	if cfg.NamespacePrefix == "" {
		cfg.NamespacePrefix = defaultNamespacePrefix
	}
	if cfg.NodeTokenTTL <= 0 {
		cfg.NodeTokenTTL = defaultNodeTokenTTL
	}
	client := cfg.HTTPClient
	if client == nil {
		var err error
		client, err = inClusterHTTPClient(cfg.CAFile)
		if err != nil {
			return nil, err
		}
	}
	return &Fleet{cfg: cfg, queries: queries, http: client}, nil
}

func (f *Fleet) Enabled() bool { return f != nil }

// Do dispatches the fleet API surface. It mirrors the remote Fleet's REST
// contract so the proxy handlers in handler/cloud_runtime.go need no changes.
func (f *Fleet) Do(ctx context.Context, req cloudruntime.Request) (*cloudruntime.Response, error) {
	if f == nil {
		return nil, cloudruntime.ErrDisabled
	}
	switch {
	case req.Path == "/healthz" || req.Path == "/readyz":
		return jsonResponse(http.StatusOK, map[string]string{"status": "ok"})
	case req.Path == "/api/v1/" && req.Method == http.MethodGet:
		return jsonResponse(http.StatusOK, map[string]string{"service": "kubefleet", "status": "ok"})
	case req.Path == "/api/v1/nodes":
		switch req.Method {
		case http.MethodGet:
			return f.listNodes(ctx)
		case http.MethodPost:
			return f.createNode(ctx, req)
		case http.MethodDelete:
			return f.deleteNode(ctx, req)
		}
	}
	return jsonResponse(http.StatusNotImplemented, map[string]string{
		"error": "operation not supported by kubefleet",
	})
}

// workspaceScope resolves the request's workspace and namespace from the
// middleware-injected context. The /api/cloud-runtime routes sit inside the
// RequireWorkspaceMember group, so both values are always present for
// well-formed requests.
func (f *Fleet) workspaceScope(ctx context.Context) (wsID, namespace string, member db.Member, resp *cloudruntime.Response) {
	wsID = middleware.WorkspaceIDFromContext(ctx)
	m, ok := middleware.MemberFromContext(ctx)
	if wsID == "" || !ok {
		r, _ := jsonResponse(http.StatusBadRequest, map[string]string{"error": "workspace context is required"})
		return "", "", db.Member{}, r
	}
	return wsID, f.cfg.NamespacePrefix + strings.ReplaceAll(wsID, "-", ""), m, nil
}

func isWorkspaceAdmin(m db.Member) bool {
	return m.Role == "owner" || m.Role == "admin"
}

// --- node CRUD ---

type createNodeRequest struct {
	Name         string `json:"name"`
	InstanceType string `json:"instance_type"`
}

func (f *Fleet) createNode(ctx context.Context, req cloudruntime.Request) (*cloudruntime.Response, error) {
	wsID, namespace, member, errResp := f.workspaceScope(ctx)
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
	nodeName, err := randomNodeName()
	if err != nil {
		return nil, err
	}
	displayName := strings.TrimSpace(body.Name)
	if displayName == "" {
		displayName = nodeName
	}
	instanceType := strings.TrimSpace(body.InstanceType)

	wsUUID, err := util.ParseUUID(wsID)
	if err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}

	// Mint the node PAT before touching Kubernetes so a half-created node
	// never runs without a revocable credential; roll the row back if any
	// k8s call fails.
	token, err := auth.GenerateCloudNodeToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(f.cfg.NodeTokenTTL)
	if _, err := f.queries.CreateCloudNodeToken(ctx, db.CreateCloudNodeTokenParams{
		TokenHash:   auth.HashToken(token),
		WorkspaceID: wsUUID,
		OwnerID:     member.UserID,
		NodeName:    nodeName,
		ExpiresAt:   pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("create cloud node token: %w", err)
	}

	cleanup := func() {
		_ = f.queries.DeleteCloudNodeTokensByNode(context.WithoutCancel(ctx), db.DeleteCloudNodeTokensByNodeParams{
			WorkspaceID: wsUUID,
			NodeName:    nodeName,
		})
	}

	if err := f.ensureNamespace(ctx, namespace, wsID); err != nil {
		cleanup()
		return nil, err
	}
	if err := f.createSecret(ctx, namespace, nodeName, token, wsID); err != nil {
		cleanup()
		return nil, err
	}
	ownerID := util.UUIDToString(member.UserID)
	if err := f.createDeployment(ctx, namespace, nodeName, displayName, instanceType, ownerID, wsID); err != nil {
		cleanup()
		_ = f.kubeDelete(ctx, secretPath(namespace, nodeName))
		return nil, err
	}

	node := nodeJSON(deployment{}, namespace, nodeName)
	node["name"] = displayName
	node["owner_id"] = ownerID
	node["instance_type"] = instanceType
	node["image_id"] = f.cfg.Image
	node["status"] = "launching"
	now := time.Now().UTC().Format(time.RFC3339)
	node["created_at"] = now
	node["updated_at"] = now
	return jsonResponse(http.StatusCreated, node)
}

func (f *Fleet) listNodes(ctx context.Context) (*cloudruntime.Response, error) {
	_, namespace, _, errResp := f.workspaceScope(ctx)
	if errResp != nil {
		return errResp, nil
	}
	list, status, err := f.kubeGetDeployments(ctx, namespace)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		// Namespace not created yet — the workspace simply has no nodes.
		return jsonResponse(http.StatusOK, []any{})
	}
	nodes := make([]map[string]any, 0, len(list.Items))
	for _, d := range list.Items {
		nodes = append(nodes, nodeJSON(d, namespace, d.Metadata.Name))
	}
	return jsonResponse(http.StatusOK, nodes)
}

type deleteNodeRequest struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
}

func (f *Fleet) deleteNode(ctx context.Context, req cloudruntime.Request) (*cloudruntime.Response, error) {
	wsID, namespace, member, errResp := f.workspaceScope(ctx)
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
	nodeName := strings.TrimSpace(body.InstanceID)
	if nodeName == "" {
		nodeName = strings.TrimSpace(body.ID)
	}
	// Node names are generated as "node-<hex>"; rejecting anything else
	// keeps arbitrary object names in the namespace out of reach.
	if !strings.HasPrefix(nodeName, "node-") {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "instance_id is required"})
	}

	if err := f.kubeDelete(ctx, deploymentPath(namespace, nodeName)); err != nil {
		return nil, err
	}
	if err := f.kubeDelete(ctx, secretPath(namespace, nodeName)); err != nil {
		return nil, err
	}
	wsUUID, err := util.ParseUUID(wsID)
	if err == nil {
		if derr := f.queries.DeleteCloudNodeTokensByNode(ctx, db.DeleteCloudNodeTokensByNodeParams{
			WorkspaceID: wsUUID,
			NodeName:    nodeName,
		}); derr != nil {
			return nil, fmt.Errorf("revoke cloud node tokens: %w", derr)
		}
	}
	return jsonResponse(http.StatusOK, map[string]string{"status": "deleted", "id": nodeName})
}

// --- kubernetes REST plumbing ---
//
// Deliberately not client-go: the five calls below are the entire API
// surface, and plain net/http keeps the dependency tree unchanged.

type deploymentList struct {
	Items []deployment `json:"items"`
}

type deployment struct {
	Metadata struct {
		Name              string            `json:"name"`
		Annotations       map[string]string `json:"annotations"`
		CreationTimestamp string            `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas int `json:"readyReplicas"`
	} `json:"status"`
}

func nodeJSON(d deployment, namespace, name string) map[string]any {
	status := "launching"
	if d.Status.ReadyReplicas >= 1 {
		status = "online"
	}
	image := ""
	if cs := d.Spec.Template.Spec.Containers; len(cs) > 0 {
		image = cs[0].Image
	}
	ann := d.Metadata.Annotations
	displayName := ann[annotationDisplayName]
	if displayName == "" {
		displayName = name
	}
	created := d.Metadata.CreationTimestamp
	return map[string]any{
		"id":            name,
		"owner_id":      ann[annotationOwnerID],
		"instance_id":   name,
		"region":        "k8s",
		"instance_type": ann[annotationInstanceType],
		"image_id":      image,
		"subnet_id":     namespace,
		"name":          displayName,
		"status":        status,
		"tags":          map[string]string{},
		"metadata":      map[string]any{},
		"created_at":    created,
		"updated_at":    created,
	}
}

func (f *Fleet) ensureNamespace(ctx context.Context, namespace, wsID string) error {
	body := map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": namespace,
			"labels": map[string]string{
				managedByLabel: managedByValue,
				workspaceLabel: wsID,
			},
		},
	}
	status, err := f.kubePost(ctx, "/api/v1/namespaces", body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusConflict {
		return fmt.Errorf("kubefleet: create namespace %s: unexpected status %d", namespace, status)
	}
	return nil
}

func secretName(nodeName string) string { return nodeName + "-token" }

func secretPath(namespace, nodeName string) string {
	return "/api/v1/namespaces/" + namespace + "/secrets/" + secretName(nodeName)
}

func deploymentPath(namespace, nodeName string) string {
	return "/apis/apps/v1/namespaces/" + namespace + "/deployments/" + nodeName
}

func (f *Fleet) createSecret(ctx context.Context, namespace, nodeName, token, wsID string) error {
	body := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      secretName(nodeName),
			"namespace": namespace,
			"labels": map[string]string{
				managedByLabel: managedByValue,
				workspaceLabel: wsID,
				nodeLabel:      nodeName,
			},
		},
		"stringData": map[string]string{"token": token},
	}
	status, err := f.kubePost(ctx, "/api/v1/namespaces/"+namespace+"/secrets", body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("kubefleet: create secret for %s: unexpected status %d", nodeName, status)
	}
	return nil
}

func (f *Fleet) createDeployment(ctx context.Context, namespace, nodeName, displayName, instanceType, ownerID, wsID string) error {
	res, ok := instanceResources[instanceType]
	if !ok {
		res = defaultResources
	}
	labels := map[string]string{
		managedByLabel: managedByValue,
		workspaceLabel: wsID,
		nodeLabel:      nodeName,
	}
	body := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      nodeName,
			"namespace": namespace,
			"labels":    labels,
			"annotations": map[string]string{
				annotationDisplayName:  displayName,
				annotationOwnerID:      ownerID,
				annotationInstanceType: instanceType,
			},
		},
		"spec": map[string]any{
			"replicas": 1,
			"selector": map[string]any{"matchLabels": map[string]string{nodeLabel: nodeName}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"containers": []map[string]any{{
						"name":  "runtime",
						"image": f.cfg.Image,
						"args":  []string{"daemon", "run"},
						"env": []map[string]any{
							{"name": "MULTICA_SERVER_URL", "value": f.cfg.ServerURL},
							{"name": "MULTICA_RUNTIME_MODE", "value": "cloud"},
							{"name": "MULTICA_WATCH_WORKSPACE_IDS", "value": wsID},
							{"name": "MULTICA_DAEMON_DEVICE_NAME", "value": displayName},
							{"name": "MULTICA_DAEMON_ID", "value": nodeName},
							{"name": "MULTICA_DAEMON_AUTO_UPDATE", "value": "false"},
							{"name": "MULTICA_AUTH_TOKEN", "valueFrom": map[string]any{
								"secretKeyRef": map[string]any{
									"name": secretName(nodeName),
									"key":  "token",
								},
							}},
						},
						"resources": map[string]any{
							"requests": map[string]string{"cpu": res.CPU, "memory": res.Memory},
							"limits":   map[string]string{"cpu": res.CPU, "memory": res.Memory},
						},
					}},
				},
			},
		},
	}
	status, err := f.kubePost(ctx, "/apis/apps/v1/namespaces/"+namespace+"/deployments", body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("kubefleet: create deployment %s: unexpected status %d", nodeName, status)
	}
	return nil
}

func (f *Fleet) kubeGetDeployments(ctx context.Context, namespace string) (deploymentList, int, error) {
	path := "/apis/apps/v1/namespaces/" + namespace + "/deployments?labelSelector=" + managedByLabel + "%3D" + managedByValue
	req, err := f.kubeRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return deploymentList{}, 0, err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return deploymentList{}, 0, fmt.Errorf("kubefleet: list deployments: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return deploymentList{}, http.StatusNotFound, nil
	}
	if resp.StatusCode != http.StatusOK {
		return deploymentList{}, 0, fmt.Errorf("kubefleet: list deployments: unexpected status %d", resp.StatusCode)
	}
	var list deploymentList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return deploymentList{}, 0, fmt.Errorf("kubefleet: decode deployment list: %w", err)
	}
	return list, http.StatusOK, nil
}

func (f *Fleet) kubePost(ctx context.Context, path string, body any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := f.kubeRequest(ctx, http.MethodPost, path, raw)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("kubefleet: %s %s: %w", http.MethodPost, path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// kubeDelete tolerates 404 so delete stays idempotent under retries.
func (f *Fleet) kubeDelete(ctx context.Context, path string) error {
	req, err := f.kubeRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return fmt.Errorf("kubefleet: delete %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("kubefleet: delete %s: unexpected status %d", path, resp.StatusCode)
	}
	return nil
}

func (f *Fleet) kubeRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var reader *strings.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	} else {
		reader = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(ctx, method, f.cfg.KubeAPIURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	// The service-account token rotates on disk (BoundServiceAccountToken);
	// reading per request keeps us current without a refresh loop. Missing
	// file is tolerated for plain-HTTP test servers.
	if raw, err := os.ReadFile(f.cfg.TokenFile); err == nil {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(raw)))
	}
	return req, nil
}

func inClusterHTTPClient(caFile string) (*http.Client, error) {
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("kubefleet: read CA file %s: %w (is the server running in-cluster?)", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("kubefleet: CA file %s contains no certificates", caFile)
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}, nil
}

// --- helpers ---

func randomNodeName() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "node-" + hex.EncodeToString(b), nil
}

func jsonResponse(status int, v any) (*cloudruntime.Response, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return &cloudruntime.Response{StatusCode: status, Header: h, Body: raw}, nil
}
