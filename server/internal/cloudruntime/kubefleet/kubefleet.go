// Package kubefleet is the Kubernetes implementation of
// cloudruntime.Provider: it provisions one node as a namespace + StatefulSet
// + PVC + secrets. The provider-agnostic parts (HTTP dispatch, workspace
// auth, node quota, mcn_ PAT minting/revoking, runtime-row cascade) live in
// the cloudruntime.Fleet adapter — see internal/cloudruntime/fleet.go. This
// package never touches the database.
//
// Model: one Kubernetes namespace per workspace, one single-replica
// StatefulSet per node running the multica daemon with a persistent
// /workspace volume — cloned repos, npm caches and agent CLI state survive
// pod restarts; deleting the node deletes the volume (EC2-terminate
// semantics via the StatefulSet PVC retention policy). Each node gets a
// locally minted mcn_ PAT (cloud_node_token table) delivered via a Secret;
// the daemon inside the pod authenticates with it, registers with
// runtime_mode=cloud, and is pinned to its workspace via
// MULTICA_WATCH_WORKSPACE_IDS.
//
// kubefleet is decoupled from the image internals: it injects only
// infrastructure env (server URL, token, workspace pinning) and lets the
// runtime image's non-interactive entrypoint (gitlab devops/cloud-runtime,
// multica-entrypoint) do the rest — derive HOME, write the codex proxy
// config from env, and start the daemon, which authenticates from
// MULTICA_AUTH_TOKEN without any interactive setup/login. All LLM config
// (base URLs, models, proxy keys) is workspace-level and reaches the pod via
// the per-namespace env secret, never from kubefleet.
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

	"github.com/multica-ai/multica/server/internal/cloudruntime"
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
	defaultDiskSizeGB      = 20

	// pullSecretName is the per-namespace dockerconfigjson secret kubefleet
	// creates when Config.PullSecretDockerConfigJSON is set. Mirrors the ops
	// deploy script, which also materializes the registry credential into
	// every workspace namespace.
	pullSecretName = "multica-registry"

	// workspaceEnvSecretName is the per-namespace secret carrying the
	// workspace-scoped env (LLM proxy keys) that admins configure via
	// /api/workspaces/{id}/cloud-runtime-env. Synced from the DB on every
	// node create; referenced via envFrom(optional) so nodes still start
	// when the workspace has no env configured.
	workspaceEnvSecretName = "multica-workspace-env"

	// quotaName is the per-namespace ResourceQuota capping node count. The
	// app-level check in createNode gives the friendly 409; the quota is the
	// race-proof backstop enforced by the API server.
	quotaName = "multica-nodes"

	defaultMaxNodesPerWorkspace = 3
)

// instanceResources maps the instance_type strings the dialog offers onto pod
// resource requests/limits, matching the real EC2 t4g shapes so the same UI
// labels mean the same capacity on both fleets. Unknown types fall back to
// the smallest preset — degrade, don't reject, so a newer frontend can't
// brick node creation.
var instanceResources = map[string]podResources{
	"t4g.medium": {CPU: "2", Memory: "4Gi"},
	"t4g.large":  {CPU: "2", Memory: "8Gi"},
}

var defaultResources = podResources{CPU: "2", Memory: "4Gi"}

type podResources struct {
	CPU    string
	Memory string
}

type Config struct {
	// Image is the container image every node pod runs — the ops runtime
	// image (multica CLI + agent CLIs + multica-entrypoint). Required.
	Image string
	// ServerURL is the multica server base URL as reachable FROM the pods
	// (e.g. the in-cluster service URL). Required.
	ServerURL string
	// KubeAPIURL, TokenFile and CAFile locate the Kubernetes API. They
	// default to the standard in-cluster values (the server runs in the same
	// cluster the nodes are created in) and are only overridden by tests.
	KubeAPIURL string
	TokenFile  string
	CAFile     string
	// Kubeconfig, when set, points the whole deployment at ONE cluster via a
	// kubeconfig (file path or inline YAML) instead of the in-cluster
	// service account. Its current-context supplies the API URL, CA trust and
	// auth (bearer token or client cert). Unset = in-cluster.
	Kubeconfig string
	// NamespacePrefix prefixes the per-workspace namespace name
	// (default "mrt-"; namespace = prefix + workspace UUID without dashes).
	NamespacePrefix string
	// StorageClass names the StorageClass for node volumes. Empty uses the
	// cluster default.
	StorageClass string
	// PullSecretDockerConfigJSON, when non-empty, is a .dockerconfigjson
	// payload materialized as an imagePullSecret in every node namespace —
	// required when Image lives in a private registry.
	PullSecretDockerConfigJSON string
	// ExtraEnv is injected into every node container via its Secret —
	// deployment-wide settings shared by all workspaces. Per-workspace
	// values (proxy keys) are delivered separately via NodeSpec.Env.
	ExtraEnv map[string]string
	// MaxNodesPerWorkspace caps nodes per workspace (default 3). Enforced
	// both app-side (friendly 409) and by a namespace ResourceQuota.
	MaxNodesPerWorkspace int
	// HTTPClient overrides the Kubernetes API client (tests). When nil a
	// client is built from CAFile.
	HTTPClient *http.Client
}

type K8sProvider struct {
	cfg  Config
	http *http.Client
	// bearerToken, when set (from a kubeconfig), is used for every request
	// instead of reading cfg.TokenFile. Empty means in-cluster (read the
	// rotating service-account token file) or client-cert auth.
	bearerToken string
}

// New validates cfg, applies defaults and returns a ready K8sProvider — the
// Kubernetes implementation of cloudruntime.Provider. Wrap it with
// cloudruntime.NewFleet to get the handler-facing adapter.
func New(cfg Config) (*K8sProvider, error) {
	if strings.TrimSpace(cfg.Image) == "" {
		return nil, fmt.Errorf("kubefleet: image is required (MULTICA_CLOUD_RUNTIME_IMAGE)")
	}
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, fmt.Errorf("kubefleet: server URL is required (MULTICA_CLOUD_RUNTIME_SERVER_URL)")
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
	if cfg.MaxNodesPerWorkspace <= 0 {
		cfg.MaxNodesPerWorkspace = defaultMaxNodesPerWorkspace
	}
	client := cfg.HTTPClient
	bearerToken := ""
	switch {
	case client != nil:
		// Test override.
	case strings.TrimSpace(cfg.Kubeconfig) != "":
		kube, err := loadKubeconfig(cfg.Kubeconfig)
		if err != nil {
			return nil, err
		}
		cfg.KubeAPIURL = kube.apiURL
		client = kube.http
		bearerToken = kube.bearerToken
	default:
		var err error
		client, err = inClusterHTTPClient(cfg.CAFile)
		if err != nil {
			return nil, err
		}
	}
	return &K8sProvider{cfg: cfg, http: client, bearerToken: bearerToken}, nil
}

// ParseExtraEnv parses the MULTICA_CLOUD_RUNTIME_EXTRA_ENV format:
// comma-separated KEY=VALUE pairs. Empty input yields nil.
func ParseExtraEnv(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	env := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("kubefleet: invalid extra env entry %q (want KEY=VALUE)", pair)
		}
		env[key] = value
	}
	return env, nil
}

func (f *K8sProvider) Name() string { return "k8s" }

// namespaceName builds a DNS-safe, readable namespace: prefix + sanitized slug
// + first 8 hex of the workspace UUID. Total stays well under the 63-char
// label limit.
func (f *K8sProvider) namespaceName(slug, wsID string) string {
	uuid8 := strings.ReplaceAll(wsID, "-", "")
	if len(uuid8) > 8 {
		uuid8 = uuid8[:8]
	}
	clean := sanitizeDNSLabel(slug)
	if clean == "" {
		return f.cfg.NamespacePrefix + uuid8
	}
	if len(clean) > 40 {
		clean = strings.Trim(clean[:40], "-")
	}
	return f.cfg.NamespacePrefix + clean + "-" + uuid8
}

// sanitizeDNSLabel lowercases and reduces s to [a-z0-9-], collapsing runs of
// other characters to a single hyphen and trimming leading/trailing hyphens.
func sanitizeDNSLabel(s string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// --- cloudruntime.Provider implementation ---

// CreateNode provisions namespace + quota + pull secret + workspace-env secret
// + node token secret + StatefulSet for one node. The adapter has already
// minted spec.Token and decrypted spec.Env. On a partial failure it best-effort
// deletes the node token secret; the shared namespace is left in place.
func (f *K8sProvider) CreateNode(ctx context.Context, spec cloudruntime.NodeSpec) (cloudruntime.Node, error) {
	ns := f.namespaceName(spec.WorkspaceSlug, spec.WorkspaceID)
	if err := f.ensureNamespace(ctx, ns, spec.WorkspaceID); err != nil {
		return cloudruntime.Node{}, err
	}
	if err := f.ensureNodeQuota(ctx, ns, spec.WorkspaceID); err != nil {
		return cloudruntime.Node{}, err
	}
	if err := f.ensurePullSecret(ctx, ns, spec.WorkspaceID); err != nil {
		return cloudruntime.Node{}, err
	}
	if err := f.writeWorkspaceEnvSecret(ctx, ns, spec.WorkspaceID, spec.Env); err != nil {
		return cloudruntime.Node{}, err
	}
	if err := f.createSecret(ctx, ns, spec.Name, spec.Token, spec.WorkspaceID); err != nil {
		return cloudruntime.Node{}, err
	}
	if err := f.createStatefulSet(ctx, ns, spec.Name, spec.DisplayName, spec.InstanceType, spec.OwnerID, spec.WorkspaceID, spec.DiskSizeGB); err != nil {
		_ = f.kubeDelete(ctx, secretPath(ns, spec.Name))
		return cloudruntime.Node{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return cloudruntime.Node{
		ID:           spec.Name,
		DisplayName:  spec.DisplayName,
		Status:       "launching",
		InstanceType: spec.InstanceType,
		ImageID:      f.cfg.Image,
		Region:       "k8s",
		SubnetID:     ns,
		OwnerID:      spec.OwnerID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (f *K8sProvider) ListNodes(ctx context.Context, workspaceID, workspaceSlug string) ([]cloudruntime.Node, error) {
	ns := f.namespaceName(workspaceSlug, workspaceID)
	list, status, err := f.kubeGetStatefulSets(ctx, ns)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	nodes := make([]cloudruntime.Node, 0, len(list.Items))
	for _, s := range list.Items {
		nodes = append(nodes, f.toNode(s, ns))
	}
	return nodes, nil
}

func (f *K8sProvider) DeleteNode(ctx context.Context, workspaceID, workspaceSlug, nodeName string) error {
	ns := f.namespaceName(workspaceSlug, workspaceID)
	// The StatefulSet's PVC retention policy (whenDeleted: Delete) removes the
	// node volume with it. Token revocation + runtime-row cascade are the
	// adapter's job.
	if err := f.kubeDelete(ctx, statefulSetPath(ns, nodeName)); err != nil {
		return err
	}
	return f.kubeDelete(ctx, secretPath(ns, nodeName))
}

func (f *K8sProvider) CountNodes(ctx context.Context, workspaceID, workspaceSlug string) (int, error) {
	ns := f.namespaceName(workspaceSlug, workspaceID)
	list, status, err := f.kubeGetStatefulSets(ctx, ns)
	if err != nil {
		return 0, err
	}
	if status == http.StatusNotFound {
		return 0, nil
	}
	return len(list.Items), nil
}

// --- kubernetes REST plumbing ---
//
// Deliberately not client-go: the handful of calls below is the entire API
// surface, and plain net/http keeps the dependency tree unchanged.

type statefulSetList struct {
	Items []statefulSet `json:"items"`
}

type statefulSet struct {
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

// toNode maps a StatefulSet into the backend-agnostic cloudruntime.Node.
func (f *K8sProvider) toNode(s statefulSet, namespace string) cloudruntime.Node {
	status := "launching"
	if s.Status.ReadyReplicas >= 1 {
		status = "online"
	}
	image := ""
	if cs := s.Spec.Template.Spec.Containers; len(cs) > 0 {
		image = cs[0].Image
	}
	ann := s.Metadata.Annotations
	created := s.Metadata.CreationTimestamp
	return cloudruntime.Node{
		ID:           s.Metadata.Name,
		DisplayName:  ann[annotationDisplayName],
		Status:       status,
		InstanceType: ann[annotationInstanceType],
		ImageID:      image,
		Region:       "k8s",
		SubnetID:     namespace,
		OwnerID:      ann[annotationOwnerID],
		CreatedAt:    created,
		UpdatedAt:    created,
	}
}

func (f *K8sProvider) ensureNamespace(ctx context.Context, namespace, wsID string) error {
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

// ensureNodeQuota installs the per-namespace ResourceQuota capping node
// count. 409 (already exists) is tolerated; a later change to
// MaxNodesPerWorkspace therefore only tightens the app-level check on
// existing namespaces, not the quota object — acceptable, since the quota
// is a backstop against races, not the configuration surface.
func (f *K8sProvider) ensureNodeQuota(ctx context.Context, namespace, wsID string) error {
	body := map[string]any{
		"apiVersion": "v1",
		"kind":       "ResourceQuota",
		"metadata": map[string]any{
			"name":      quotaName,
			"namespace": namespace,
			"labels": map[string]string{
				managedByLabel: managedByValue,
				workspaceLabel: wsID,
			},
		},
		"spec": map[string]any{
			"hard": map[string]string{
				"count/statefulsets.apps": fmt.Sprintf("%d", f.cfg.MaxNodesPerWorkspace),
			},
		},
	}
	status, err := f.kubePost(ctx, "/api/v1/namespaces/"+namespace+"/resourcequotas", body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusConflict {
		return fmt.Errorf("kubefleet: create resource quota in %s: unexpected status %d", namespace, status)
	}
	return nil
}

// writeWorkspaceEnvSecret materializes the workspace env (LLM proxy keys,
// already decrypted by the adapter) into the namespace on every node create,
// so a key rotated in settings reaches the next node without ops involvement.
// Existing nodes keep the env they booted with until their pod restarts. An
// empty env leaves any manually-managed secret untouched.
func (f *K8sProvider) writeWorkspaceEnvSecret(ctx context.Context, namespace, wsID string, env map[string]string) error {
	if len(env) == 0 {
		return nil
	}
	body := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      workspaceEnvSecretName,
			"namespace": namespace,
			"labels": map[string]string{
				managedByLabel: managedByValue,
				workspaceLabel: wsID,
			},
		},
		"stringData": env,
	}
	status, err := f.kubePost(ctx, "/api/v1/namespaces/"+namespace+"/secrets", body)
	if err != nil {
		return err
	}
	if status == http.StatusConflict {
		status, err = f.kubePut(ctx, "/api/v1/namespaces/"+namespace+"/secrets/"+workspaceEnvSecretName, body)
		if err != nil {
			return err
		}
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("kubefleet: sync workspace env secret in %s: unexpected status %d", namespace, status)
	}
	return nil
}

// ensurePullSecret materializes the registry credential into the node
// namespace, mirroring the ops deploy script. No-op when the deployment
// doesn't configure one (public registry).
func (f *K8sProvider) ensurePullSecret(ctx context.Context, namespace, wsID string) error {
	if f.cfg.PullSecretDockerConfigJSON == "" {
		return nil
	}
	body := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "kubernetes.io/dockerconfigjson",
		"metadata": map[string]any{
			"name":      pullSecretName,
			"namespace": namespace,
			"labels": map[string]string{
				managedByLabel: managedByValue,
				workspaceLabel: wsID,
			},
		},
		"stringData": map[string]string{".dockerconfigjson": f.cfg.PullSecretDockerConfigJSON},
	}
	status, err := f.kubePost(ctx, "/api/v1/namespaces/"+namespace+"/secrets", body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusConflict {
		return fmt.Errorf("kubefleet: create pull secret in %s: unexpected status %d", namespace, status)
	}
	return nil
}

func secretName(nodeName string) string { return nodeName + "-token" }

func secretPath(namespace, nodeName string) string {
	return "/api/v1/namespaces/" + namespace + "/secrets/" + secretName(nodeName)
}

func statefulSetPath(namespace, nodeName string) string {
	return "/apis/apps/v1/namespaces/" + namespace + "/statefulsets/" + nodeName
}

// createSecret stores the node PAT plus the deployment-wide extra env
// (LLM proxy endpoints/keys) so none of them appear inline in the
// StatefulSet spec.
func (f *K8sProvider) createSecret(ctx context.Context, namespace, nodeName, token, wsID string) error {
	data := map[string]string{"token": token}
	for k, v := range f.cfg.ExtraEnv {
		data[k] = v
	}
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
		"stringData": data,
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

func (f *K8sProvider) createStatefulSet(ctx context.Context, namespace, nodeName, displayName, instanceType, ownerID, wsID string, diskSizeGB int) error {
	res, ok := instanceResources[instanceType]
	if !ok {
		res = defaultResources
	}
	// Owner as a label (not just annotation) so ops can filter:
	// kubectl get sts -l multica.io/owner-id=<uuid>. The selector below
	// matches on nodeLabel only, so extra labels stay mutable.
	labels := map[string]string{
		managedByLabel:    managedByValue,
		workspaceLabel:    wsID,
		nodeLabel:         nodeName,
		annotationOwnerID: ownerID,
	}
	tokenRef := map[string]any{
		"secretKeyRef": map[string]any{"name": secretName(nodeName), "key": "token"},
	}
	// Infrastructure env only. Everything the runtime image needs to set up
	// itself — HOME, agent PATH, codex proxy config, IS_SANDBOX — is the
	// image entrypoint's job (kubefleet is decoupled from image internals).
	// All LLM config (base URLs, models, proxy keys) is workspace-level and
	// arrives via the envFrom workspace secret below, not from here.
	env := []map[string]any{
		{"name": "MULTICA_SERVER_URL", "value": f.cfg.ServerURL},
		{"name": "MULTICA_APP_URL", "value": f.cfg.ServerURL},
		// Names the persistent HOME dir (/workspace/<wsid>); the entrypoint
		// derives HOME from it.
		{"name": "MULTICA_WORKSPACE", "value": wsID},
		{"name": "MULTICA_RUNTIME_MODE", "value": "cloud"},
		{"name": "MULTICA_WATCH_WORKSPACE_IDS", "value": wsID},
		{"name": "MULTICA_DAEMON_DEVICE_NAME", "value": displayName},
		{"name": "MULTICA_DAEMON_ID", "value": nodeName},
		{"name": "MULTICA_DAEMON_AUTO_UPDATE", "value": "false"},
		// The node's mcn_ PAT. MULTICA_AUTH_TOKEN is what the daemon reads
		// for non-interactive auth; MULTICA_API_TOKEN is the entrypoint's
		// fallback name.
		{"name": "MULTICA_API_TOKEN", "valueFrom": tokenRef},
		{"name": "MULTICA_AUTH_TOKEN", "valueFrom": tokenRef},
	}
	for key := range f.cfg.ExtraEnv {
		env = append(env, map[string]any{
			"name": key,
			"valueFrom": map[string]any{
				"secretKeyRef": map[string]any{"name": secretName(nodeName), "key": key},
			},
		})
	}

	podSpec := map[string]any{
		"containers": []map[string]any{{
			"name":  "runtime",
			"image": f.cfg.Image,
			// No command override: the runtime image's entrypoint is
			// non-interactive and self-contained (writes codex config,
			// sets HOME, starts the daemon which authenticates from
			// MULTICA_AUTH_TOKEN). kubefleet only supplies env.
			"env": env,
			// Workspace-scoped env (per-workspace LLM proxy keys) rides in
			// a shared namespace secret synced from the admin settings API.
			// optional: nodes still start when nothing is configured.
			"envFrom": []map[string]any{{
				"secretRef": map[string]any{"name": workspaceEnvSecretName, "optional": true},
			}},
			"resources": map[string]any{
				"requests": map[string]string{"cpu": res.CPU, "memory": res.Memory},
				"limits":   map[string]string{"cpu": res.CPU, "memory": res.Memory},
			},
			"volumeMounts": []map[string]any{{
				"name":      "workspace",
				"mountPath": "/workspace",
			}},
			"workingDir": "/workspace",
		}},
	}
	if f.cfg.PullSecretDockerConfigJSON != "" {
		podSpec["imagePullSecrets"] = []map[string]any{{"name": pullSecretName}}
	}

	pvcSpec := map[string]any{
		"accessModes": []string{"ReadWriteOnce"},
		"resources": map[string]any{
			"requests": map[string]string{"storage": fmt.Sprintf("%dGi", diskSizeGB)},
		},
	}
	if f.cfg.StorageClass != "" {
		pvcSpec["storageClassName"] = f.cfg.StorageClass
	}

	body := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "StatefulSet",
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
			"replicas":    1,
			"serviceName": nodeName,
			"selector":    map[string]any{"matchLabels": map[string]string{nodeLabel: nodeName}},
			// Deleting the node deletes its volume — EC2-terminate
			// semantics. Scale-down keeps it (we never scale, but Retain
			// is the safer default if someone does by hand).
			"persistentVolumeClaimRetentionPolicy": map[string]any{
				"whenDeleted": "Delete",
				"whenScaled":  "Retain",
			},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec":     podSpec,
			},
			"volumeClaimTemplates": []map[string]any{{
				"metadata": map[string]any{"name": "workspace", "labels": labels},
				"spec":     pvcSpec,
			}},
		},
	}
	status, err := f.kubePost(ctx, "/apis/apps/v1/namespaces/"+namespace+"/statefulsets", body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("kubefleet: create statefulset %s: unexpected status %d", nodeName, status)
	}
	return nil
}

func (f *K8sProvider) kubeGetStatefulSets(ctx context.Context, namespace string) (statefulSetList, int, error) {
	path := "/apis/apps/v1/namespaces/" + namespace + "/statefulsets?labelSelector=" + managedByLabel + "%3D" + managedByValue
	req, err := f.kubeRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return statefulSetList{}, 0, err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return statefulSetList{}, 0, fmt.Errorf("kubefleet: list statefulsets: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return statefulSetList{}, http.StatusNotFound, nil
	}
	if resp.StatusCode != http.StatusOK {
		return statefulSetList{}, 0, fmt.Errorf("kubefleet: list statefulsets: unexpected status %d", resp.StatusCode)
	}
	var list statefulSetList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return statefulSetList{}, 0, fmt.Errorf("kubefleet: decode statefulset list: %w", err)
	}
	return list, http.StatusOK, nil
}

func (f *K8sProvider) kubePost(ctx context.Context, path string, body any) (int, error) {
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

// kubePut replaces an existing object (create-or-replace second leg).
func (f *K8sProvider) kubePut(ctx context.Context, path string, body any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := f.kubeRequest(ctx, http.MethodPut, path, raw)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("kubefleet: %s %s: %w", http.MethodPut, path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// kubeDelete tolerates 404 so delete stays idempotent under retries.
func (f *K8sProvider) kubeDelete(ctx context.Context, path string) error {
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

func (f *K8sProvider) kubeRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, f.cfg.KubeAPIURL+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	switch {
	case f.bearerToken != "":
		// Static token from a kubeconfig.
		req.Header.Set("Authorization", "Bearer "+f.bearerToken)
	default:
		// In-cluster: the service-account token rotates on disk
		// (BoundServiceAccountToken); reading per request keeps us current
		// without a refresh loop. Missing file is tolerated for plain-HTTP
		// test servers and client-cert kubeconfigs.
		if raw, err := os.ReadFile(f.cfg.TokenFile); err == nil {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(raw)))
		}
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
