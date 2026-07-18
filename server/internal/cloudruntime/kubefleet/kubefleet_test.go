package kubefleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
)

// fakeKube is a minimal Kubernetes API double: it records created objects and
// serves the statefulset list back with readyReplicas injected.
type fakeKube struct {
	server       *httptest.Server
	namespaces   []string
	secrets      []map[string]any
	quotas       []map[string]any
	statefulSets []map[string]any
	deletedPods  []string
	patches      []map[string]any
	// failStatefulSetCreate makes POST .../statefulsets return 500 to test rollback.
	failStatefulSetCreate bool
}

func newFakeKube(t *testing.T) *fakeKube {
	t.Helper()
	f := &fakeKube{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		name := func() string {
			if m, ok := body["metadata"].(map[string]any); ok {
				if n, ok := m["name"].(string); ok {
					return n
				}
			}
			return ""
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces":
			for _, e := range f.namespaces {
				if e == name() {
					w.WriteHeader(http.StatusConflict)
					return
				}
			}
			f.namespaces = append(f.namespaces, name())
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/secrets"):
			for _, e := range f.secrets {
				if e["metadata"].(map[string]any)["name"] == name() {
					w.WriteHeader(http.StatusConflict)
					return
				}
			}
			f.secrets = append(f.secrets, body)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/secrets/"):
			for i, e := range f.secrets {
				if e["metadata"].(map[string]any)["name"] == name() {
					f.secrets[i] = body
					w.WriteHeader(http.StatusOK)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/resourcequotas"):
			f.quotas = append(f.quotas, body)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/statefulsets"):
			if f.failStatefulSetCreate {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			body["status"] = map[string]any{"readyReplicas": 1}
			f.statefulSets = append(f.statefulSets, body)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/statefulsets"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": f.statefulSets})
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/statefulsets/"):
			f.patches = append(f.patches, body)
			name := lastPathSegment(r.URL.Path)
			for i, item := range f.statefulSets {
				if item["metadata"].(map[string]any)["name"] != name {
					continue
				}
				mergeRuntimeContainerPatch(item, body)
				f.statefulSets[i] = item
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/pods/"):
			f.deletedPods = append(f.deletedPods, lastPathSegment(r.URL.Path))
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			name := lastPathSegment(r.URL.Path)
			f.secrets = deleteNamedObject(f.secrets, name)
			f.statefulSets = deleteNamedObject(f.statefulSets, name)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func deleteNamedObject(items []map[string]any, name string) []map[string]any {
	out := items[:0]
	for _, item := range items {
		if meta, ok := item["metadata"].(map[string]any); ok {
			if n, ok := meta["name"].(string); ok && n == name {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func mergeRuntimeContainerPatch(item, patch map[string]any) {
	patchSpec := patch["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	patchContainer := patchSpec["containers"].([]any)[0].(map[string]any)
	itemSpec := item["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	containers := itemSpec["containers"].([]any)
	for _, c := range containers {
		container := c.(map[string]any)
		if container["name"] != patchContainer["name"] {
			continue
		}
		for k, v := range patchContainer {
			container[k] = v
		}
		return
	}
	itemSpec["containers"] = append(containers, patchContainer)
}

func lastPathSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func newProvider(t *testing.T, kube *fakeKube, cfg Config) *K8sProvider {
	t.Helper()
	cfg.Image = "registry.example.com/multica-runtime:test"
	cfg.ServerURL = "http://multica-server.multica.svc:8080"
	cfg.KubeAPIURL = kube.server.URL
	cfg.TokenFile = "/nonexistent/token"
	cfg.HTTPClient = kube.server.Client()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

const (
	testWS   = "bed314a3-0eaf-498f-959b-7c6c8fb6c3d8"
	testSlug = "w3-test"
	wantNS   = "mrt-w3-test-bed314a3"
)

func sampleSpec() cloudruntime.NodeSpec {
	return cloudruntime.NodeSpec{
		WorkspaceID:   testWS,
		WorkspaceSlug: testSlug,
		Name:          "node-abc12345",
		DisplayName:   "My Cloud Box",
		OwnerID:       "owner-1",
		InstanceType:  "t4g.large",
		DiskSizeGB:    32,
		Token:         "mcn_testtoken",
		Env:           map[string]string{"ANTHROPIC_AUTH_TOKEN": "sk-ws-key"},
	}
}

func TestK8sProvider_CreateAndList(t *testing.T) {
	kube := newFakeKube(t)
	p := newProvider(t, kube, Config{})

	node, err := p.CreateNode(context.Background(), sampleSpec())
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if node.ID != "node-abc12345" || node.DisplayName != "My Cloud Box" || node.Status != "launching" {
		t.Fatalf("node = %+v", node)
	}
	if node.SubnetID != wantNS {
		t.Fatalf("namespace = %q, want %q", node.SubnetID, wantNS)
	}
	if len(kube.namespaces) != 1 || kube.namespaces[0] != wantNS {
		t.Fatalf("namespaces = %v", kube.namespaces)
	}
	if len(kube.quotas) != 1 {
		t.Fatalf("expected 1 resource quota, got %d", len(kube.quotas))
	}

	// Two secrets: workspace env (from spec.Env) + node token.
	var wsEnv, nodeTok map[string]any
	for _, s := range kube.secrets {
		switch s["metadata"].(map[string]any)["name"] {
		case workspaceEnvSecretName:
			wsEnv = s
		case "node-abc12345-token":
			nodeTok = s
		}
	}
	if wsEnv == nil || nodeTok == nil {
		t.Fatalf("missing secrets; got %d", len(kube.secrets))
	}
	if wsEnv["stringData"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"] != "sk-ws-key" {
		t.Fatalf("workspace env secret wrong: %v", wsEnv["stringData"])
	}
	if nodeTok["stringData"].(map[string]any)["token"] != "mcn_testtoken" {
		t.Fatalf("node token secret wrong")
	}

	// StatefulSet: infra env, no command override, no inlined LLM config, PVC.
	stsJSON, _ := json.Marshal(kube.statefulSets[0])
	for _, want := range []string{
		`"MULTICA_AUTH_TOKEN"`, `"MULTICA_WORKSPACE"`, `"MULTICA_RUNTIME_MODE"`,
		`"multica-workspace-env"`, `"storage":"32Gi"`, `"whenDeleted":"Delete"`,
	} {
		if !strings.Contains(string(stsJSON), want) {
			t.Fatalf("statefulset missing %s: %s", want, stsJSON)
		}
	}
	for _, forbidden := range []string{`"command"`, `"MULTICA_CLAUDE_MODEL"`, `"IS_SANDBOX"`} {
		if strings.Contains(string(stsJSON), forbidden) {
			t.Fatalf("statefulset must not set %s (image owns it): %s", forbidden, stsJSON)
		}
	}

	// List + Count reflect the created node.
	nodes, err := p.ListNodes(context.Background(), testWS, testSlug)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Status != "online" || nodes[0].SubnetID != wantNS {
		t.Fatalf("ListNodes = %+v", nodes)
	}
	if n, _ := p.CountNodes(context.Background(), testWS, testSlug); n != 1 {
		t.Fatalf("CountNodes = %d, want 1", n)
	}

	// Delete is idempotent and tolerant.
	if err := p.DeleteNode(context.Background(), testWS, testSlug, "node-abc12345"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
}

func TestK8sProvider_SyncWorkspaceEnvPatchesExistingStatefulSets(t *testing.T) {
	kube := newFakeKube(t)
	p := newProvider(t, kube, Config{})

	if _, err := p.CreateNode(context.Background(), sampleSpec()); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	stsSpec := kube.statefulSets[0]["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	container := stsSpec["containers"].([]any)[0].(map[string]any)
	delete(container, "envFrom")
	container["image"] = "registry.example.com/multica-runtime:old"

	if err := p.SyncWorkspaceEnv(context.Background(), testWS, testSlug, map[string]string{
		"CODEX_BASE_URL":      "https://llm-gateway.example/v1",
		"MULTICA_CODEX_MODEL": "gpt-5.5",
	}); err != nil {
		t.Fatalf("SyncWorkspaceEnv: %v", err)
	}
	if len(kube.patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(kube.patches))
	}
	gotJSON, _ := json.Marshal(kube.statefulSets[0])
	for _, want := range []string{
		`"image":"registry.example.com/multica-runtime:test"`,
		`"envFrom":[{"secretRef":{"name":"multica-workspace-env","optional":true}}]`,
	} {
		if !strings.Contains(string(gotJSON), want) {
			t.Fatalf("statefulset missing patched %s: %s", want, gotJSON)
		}
	}
}

func TestK8sProvider_PullSecretAndExtraEnv(t *testing.T) {
	kube := newFakeKube(t)
	p := newProvider(t, kube, Config{
		PullSecretDockerConfigJSON: `{"auths":{"registry.example.com":{"auth":"Zm9v"}}}`,
		ExtraEnv:                   map[string]string{"ANTHROPIC_BASE_URL": "https://llm-proxy.example.com"},
	})
	if _, err := p.CreateNode(context.Background(), sampleSpec()); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	var pull map[string]any
	for _, s := range kube.secrets {
		if s["metadata"].(map[string]any)["name"] == pullSecretName {
			pull = s
		}
	}
	if pull == nil || pull["type"] != "kubernetes.io/dockerconfigjson" {
		t.Fatalf("pull secret missing/wrong: %v", pull)
	}
	stsJSON, _ := json.Marshal(kube.statefulSets[0])
	if !strings.Contains(string(stsJSON), `"imagePullSecrets"`) || !strings.Contains(string(stsJSON), `"ANTHROPIC_BASE_URL"`) {
		t.Fatalf("statefulset missing pull secret / extra env: %s", stsJSON)
	}
}

func TestK8sProvider_EmptyWorkspaceEnvDeletesStaleSecret(t *testing.T) {
	kube := newFakeKube(t)
	p := newProvider(t, kube, Config{})
	if _, err := p.CreateNode(context.Background(), sampleSpec()); err != nil {
		t.Fatalf("CreateNode with env: %v", err)
	}
	var found bool
	for _, s := range kube.secrets {
		if s["metadata"].(map[string]any)["name"] == workspaceEnvSecretName {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected workspace env secret after env create")
	}

	spec := sampleSpec()
	spec.Name = "node-emptyenv"
	spec.Token = "mcn_emptyenv"
	spec.Env = nil
	if _, err := p.CreateNode(context.Background(), spec); err != nil {
		t.Fatalf("CreateNode without env: %v", err)
	}
	for _, s := range kube.secrets {
		if s["metadata"].(map[string]any)["name"] == workspaceEnvSecretName {
			t.Fatalf("stale workspace env secret was not deleted: %v", s)
		}
	}
}

func TestK8sProvider_SyncWorkspaceEnvUpdatesSecret(t *testing.T) {
	kube := newFakeKube(t)
	p := newProvider(t, kube, Config{})
	if _, err := p.CreateNode(context.Background(), sampleSpec()); err != nil {
		t.Fatalf("CreateNode with env: %v", err)
	}

	if err := p.SyncWorkspaceEnv(context.Background(), testWS, testSlug, map[string]string{
		"CODEX_BASE_URL": "https://proxy.example/v1",
	}); err != nil {
		t.Fatalf("SyncWorkspaceEnv update: %v", err)
	}
	var wsEnv map[string]any
	for _, s := range kube.secrets {
		if s["metadata"].(map[string]any)["name"] == workspaceEnvSecretName {
			wsEnv = s
		}
	}
	if wsEnv == nil || wsEnv["stringData"].(map[string]any)["CODEX_BASE_URL"] != "https://proxy.example/v1" {
		t.Fatalf("workspace env secret not updated: %v", wsEnv)
	}

	if err := p.SyncWorkspaceEnv(context.Background(), testWS, testSlug, nil); err != nil {
		t.Fatalf("SyncWorkspaceEnv clear: %v", err)
	}
	for _, s := range kube.secrets {
		if s["metadata"].(map[string]any)["name"] == workspaceEnvSecretName {
			t.Fatalf("workspace env secret not deleted after clear: %v", s)
		}
	}
}

func TestK8sProvider_RestartNodeDeletesPodOnly(t *testing.T) {
	kube := newFakeKube(t)
	p := newProvider(t, kube, Config{})
	if _, err := p.CreateNode(context.Background(), sampleSpec()); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	if err := p.RestartNode(context.Background(), testWS, testSlug, "node-abc12345"); err != nil {
		t.Fatalf("RestartNode: %v", err)
	}
	if len(kube.deletedPods) != 1 || kube.deletedPods[0] != "node-abc12345-0" {
		t.Fatalf("deleted pods = %v", kube.deletedPods)
	}
	if len(kube.statefulSets) != 1 {
		t.Fatalf("restart must keep statefulset/PVC, got statefulsets=%d", len(kube.statefulSets))
	}
	var tokenFound bool
	for _, s := range kube.secrets {
		if s["metadata"].(map[string]any)["name"] == "node-abc12345-token" {
			tokenFound = true
		}
	}
	if !tokenFound {
		t.Fatalf("restart must keep node token secret")
	}
}

func TestK8sProvider_RollsBackNothingOnStatefulSetFailure(t *testing.T) {
	kube := newFakeKube(t)
	kube.failStatefulSetCreate = true
	p := newProvider(t, kube, Config{})
	if _, err := p.CreateNode(context.Background(), sampleSpec()); err == nil {
		t.Fatal("expected CreateNode to fail when statefulset create fails")
	}
	// The provider best-effort deletes the node token secret it created; the
	// adapter (not tested here) revokes the DB token row.
}

func TestNamespaceName(t *testing.T) {
	p := &K8sProvider{cfg: Config{NamespacePrefix: "mrt-"}}
	wsID := "bed314a3-0eaf-498f-959b-7c6c8fb6c3d8"
	cases := map[string]string{
		"my-workspace":   "mrt-my-workspace-bed314a3",
		"My Workspace!!": "mrt-my-workspace-bed314a3",
		"  Über Space  ": "mrt-ber-space-bed314a3",
		"":               "mrt-bed314a3",
		"---weird---":    "mrt-weird-bed314a3",
	}
	for slug, want := range cases {
		if got := p.namespaceName(slug, wsID); got != want {
			t.Fatalf("namespaceName(%q) = %q, want %q", slug, got, want)
		}
	}
}

func TestParseExtraEnv(t *testing.T) {
	env, err := ParseExtraEnv(" A=1, B=x=y ,")
	if err != nil {
		t.Fatalf("ParseExtraEnv: %v", err)
	}
	if env["A"] != "1" || env["B"] != "x=y" || len(env) != 2 {
		t.Fatalf("env = %v", env)
	}
	if got, err := ParseExtraEnv(""); err != nil || got != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
	if _, err := ParseExtraEnv("NOEQUALS"); err == nil {
		t.Fatal("expected error for entry without =")
	}
}
