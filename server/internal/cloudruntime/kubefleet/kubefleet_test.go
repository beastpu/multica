package kubefleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	testPool        *pgxpool.Pool
	testQueries     *db.Queries
	testUserID      string
	testWorkspaceID string
)

const (
	kubefleetTestEmail = "kubefleet-test@multica.ai"
	kubefleetTestSlug  = "kubefleet-tests"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Skipping tests: could not connect to database: %v\n", err)
		os.Exit(0)
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("Skipping tests: database not reachable: %v\n", err)
		pool.Close()
		os.Exit(0)
	}
	testPool = pool
	testQueries = db.New(pool)

	if err := setupFixture(ctx); err != nil {
		fmt.Printf("Failed to set up kubefleet test fixture: %v\n", err)
		pool.Close()
		os.Exit(1)
	}
	code := m.Run()
	if err := cleanupFixture(context.Background()); err != nil {
		fmt.Printf("Failed to clean up kubefleet test fixture: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	pool.Close()
	os.Exit(code)
}

func setupFixture(ctx context.Context) error {
	if err := cleanupFixture(ctx); err != nil {
		return err
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Kubefleet Test User", kubefleetTestEmail).Scan(&testUserID); err != nil {
		return err
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, "Kubefleet Tests", kubefleetTestSlug, "Temporary workspace for kubefleet tests", "KFT").Scan(&testWorkspaceID); err != nil {
		return err
	}
	_, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, testWorkspaceID, testUserID)
	return err
}

func cleanupFixture(ctx context.Context) error {
	if _, err := testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, kubefleetTestSlug); err != nil {
		return err
	}
	_, err := testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, kubefleetTestEmail)
	return err
}

// fakeKube is a minimal Kubernetes API double: it records created objects and
// serves the deployment list back with readyReplicas injected.
type fakeKube struct {
	server       *httptest.Server
	namespaces   []string
	secrets      []map[string]any
	statefulSets []map[string]any
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
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces":
			name := body["metadata"].(map[string]any)["name"].(string)
			for _, existing := range f.namespaces {
				if existing == name {
					w.WriteHeader(http.StatusConflict)
					return
				}
			}
			f.namespaces = append(f.namespaces, name)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/secrets"):
			f.secrets = append(f.secrets, body)
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
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func newTestFleet(t *testing.T, kube *fakeKube) *Fleet {
	t.Helper()
	fleet, err := New(Config{
		Image:      "registry.example.com/multica-runtime:test",
		ServerURL:  "http://multica-server.multica.svc:8080",
		KubeAPIURL: kube.server.URL,
		TokenFile:  "/nonexistent/token",
		HTTPClient: kube.server.Client(),
	}, testQueries)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fleet
}

func memberContext(role string) context.Context {
	uid, _ := util.ParseUUID(testUserID)
	wsID, _ := util.ParseUUID(testWorkspaceID)
	return middleware.SetMemberContext(context.Background(), testWorkspaceID, db.Member{
		WorkspaceID: wsID,
		UserID:      uid,
		Role:        role,
	})
}

func doJSON(t *testing.T, f *Fleet, ctx context.Context, method, path string, body any) (*cloudruntime.Response, map[string]any) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	resp, err := f.Do(ctx, cloudruntime.Request{Method: method, Path: path, Body: raw})
	if err != nil {
		t.Fatalf("Do %s %s: %v", method, path, err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(resp.Body, &decoded)
	return resp, decoded
}

func countNodeTokens(t *testing.T, nodeName string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM cloud_node_token WHERE workspace_id = $1 AND node_name = $2`,
		testWorkspaceID, nodeName).Scan(&n); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	return n
}

func TestKubefleet_CreateListDelete(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	kube := newFakeKube(t)
	fleet := newTestFleet(t, kube)
	ctx := memberContext("admin")

	// Create
	resp, node := doJSON(t, fleet, ctx, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name":          "My Cloud Box",
		"instance_type": "t4g.large",
		"disk_size_gb":  32,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d body = %s", resp.StatusCode, resp.Body)
	}
	nodeName, _ := node["instance_id"].(string)
	if !strings.HasPrefix(nodeName, "node-") {
		t.Fatalf("create: instance_id = %q, want node-* prefix", nodeName)
	}
	if node["name"] != "My Cloud Box" || node["status"] != "launching" || node["owner_id"] != testUserID {
		t.Fatalf("create: unexpected node payload %v", node)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM cloud_node_token WHERE workspace_id = $1`, testWorkspaceID)
	})
	if got := countNodeTokens(t, nodeName); got != 1 {
		t.Fatalf("expected 1 minted node token, got %d", got)
	}
	wantNS := "mrt-" + strings.ReplaceAll(testWorkspaceID, "-", "")
	if len(kube.namespaces) != 1 || kube.namespaces[0] != wantNS {
		t.Fatalf("namespaces = %v, want [%s]", kube.namespaces, wantNS)
	}
	if len(kube.secrets) != 1 || len(kube.statefulSets) != 1 {
		t.Fatalf("expected 1 secret + 1 statefulset, got %d/%d", len(kube.secrets), len(kube.statefulSets))
	}
	// The secret must carry an mcn_ token; the statefulset must reference it
	// via secretKeyRef rather than inlining the value.
	secretToken := kube.secrets[0]["stringData"].(map[string]any)["token"].(string)
	if !strings.HasPrefix(secretToken, "mcn_") {
		t.Fatalf("secret token = %q, want mcn_ prefix", secretToken)
	}
	stsJSON, _ := json.Marshal(kube.statefulSets[0])
	if strings.Contains(string(stsJSON), secretToken) {
		t.Fatalf("statefulset spec inlines the node token")
	}
	// Ops image contract: env-only (no container args/command), token under
	// MULTICA_API_TOKEN, workspace pinning, and a PVC sized from the request.
	for _, want := range []string{
		`"MULTICA_API_TOKEN"`, `"MULTICA_AUTH_TOKEN"`, `"MULTICA_WORKSPACE"`,
		`"MULTICA_RUNTIME_MODE"`, `"MULTICA_WATCH_WORKSPACE_IDS"`,
		`"volumeClaimTemplates"`, `"storage":"32Gi"`, `"whenDeleted":"Delete"`,
	} {
		if !strings.Contains(string(stsJSON), want) {
			t.Fatalf("statefulset spec missing %s: %s", want, stsJSON)
		}
	}
	for _, forbidden := range []string{`"args"`, `"command"`} {
		if strings.Contains(string(stsJSON), forbidden) {
			t.Fatalf("statefulset spec must not set container %s (image entrypoint owns startup): %s", forbidden, stsJSON)
		}
	}

	// Second create reuses the namespace (fake returns 409 conflict).
	resp2, node2 := doJSON(t, fleet, ctx, http.MethodPost, "/api/v1/nodes", map[string]any{"instance_type": "t4g.medium"})
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("second create: status = %d", resp2.StatusCode)
	}
	if len(kube.namespaces) != 1 {
		t.Fatalf("second create should reuse namespace, got %v", kube.namespaces)
	}

	// List
	listResp, _ := doJSON(t, fleet, ctx, http.MethodGet, "/api/v1/nodes", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d", listResp.StatusCode)
	}
	var nodes []map[string]any
	if err := json.Unmarshal(listResp.Body, &nodes); err != nil {
		t.Fatalf("list: decode: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("list: got %d nodes, want 2", len(nodes))
	}
	if nodes[0]["status"] != "online" {
		t.Fatalf("list: status = %v, want online (readyReplicas=1)", nodes[0]["status"])
	}
	if nodes[0]["subnet_id"] != wantNS {
		t.Fatalf("list: subnet_id = %v, want namespace %s", nodes[0]["subnet_id"], wantNS)
	}

	// Delete
	delResp, _ := doJSON(t, fleet, ctx, http.MethodDelete, "/api/v1/nodes", map[string]any{"instance_id": nodeName})
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete: status = %d body = %s", delResp.StatusCode, delResp.Body)
	}
	if got := countNodeTokens(t, nodeName); got != 0 {
		t.Fatalf("delete must revoke node tokens, still %d rows", got)
	}
	_ = node2
}

func TestKubefleet_PullSecretAndExtraEnv(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	kube := newFakeKube(t)
	fleet, err := New(Config{
		Image:                      "registry.example.com/multica-runtime:test",
		ServerURL:                  "http://multica-server.multica.svc:8080",
		KubeAPIURL:                 kube.server.URL,
		TokenFile:                  "/nonexistent/token",
		HTTPClient:                 kube.server.Client(),
		PullSecretDockerConfigJSON: `{"auths":{"registry.example.com":{"auth":"Zm9v"}}}`,
		ExtraEnv:                   map[string]string{"ANTHROPIC_BASE_URL": "https://llm-proxy.example.com"},
	}, testQueries)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM cloud_node_token WHERE workspace_id = $1`, testWorkspaceID)
	})

	resp, _ := doJSON(t, fleet, memberContext("admin"), http.MethodPost, "/api/v1/nodes", map[string]any{"instance_type": "t4g.medium"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d body = %s", resp.StatusCode, resp.Body)
	}
	// Two secrets: the registry pull secret plus the node token secret,
	// which must also carry the extra env value.
	if len(kube.secrets) != 2 {
		t.Fatalf("expected pull secret + node secret, got %d", len(kube.secrets))
	}
	pull := kube.secrets[0]
	if pull["type"] != "kubernetes.io/dockerconfigjson" {
		t.Fatalf("first secret type = %v, want dockerconfigjson", pull["type"])
	}
	nodeSecret := kube.secrets[1]["stringData"].(map[string]any)
	if nodeSecret["ANTHROPIC_BASE_URL"] != "https://llm-proxy.example.com" {
		t.Fatalf("node secret missing extra env: %v", nodeSecret)
	}
	stsJSON, _ := json.Marshal(kube.statefulSets[0])
	for _, want := range []string{`"imagePullSecrets"`, `"multica-registry"`, `"ANTHROPIC_BASE_URL"`} {
		if !strings.Contains(string(stsJSON), want) {
			t.Fatalf("statefulset spec missing %s", want)
		}
	}
	if strings.Contains(string(stsJSON), "llm-proxy.example.com") {
		t.Fatalf("extra env value must be referenced via secretKeyRef, not inlined")
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
		t.Fatalf("empty input: %v %v", got, err)
	}
	if _, err := ParseExtraEnv("NOEQUALS"); err == nil {
		t.Fatal("expected error for entry without =")
	}
}

func TestKubefleet_WriteOpsRequireAdmin(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	kube := newFakeKube(t)
	fleet := newTestFleet(t, kube)
	ctx := memberContext("member")

	resp, _ := doJSON(t, fleet, ctx, http.MethodPost, "/api/v1/nodes", map[string]any{"instance_type": "t4g.medium"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member create: status = %d, want 403", resp.StatusCode)
	}
	resp, _ = doJSON(t, fleet, ctx, http.MethodDelete, "/api/v1/nodes", map[string]any{"instance_id": "node-deadbeef"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member delete: status = %d, want 403", resp.StatusCode)
	}
	// Reads stay member-accessible.
	resp, _ = doJSON(t, fleet, ctx, http.MethodGet, "/api/v1/nodes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("member list: status = %d, want 200", resp.StatusCode)
	}
}

func TestKubefleet_MissingWorkspaceContext(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	fleet := newTestFleet(t, newFakeKube(t))
	resp, err := fleet.Do(context.Background(), cloudruntime.Request{Method: http.MethodGet, Path: "/api/v1/nodes"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestKubefleet_CreateRollsBackTokenOnStatefulSetFailure(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	kube := newFakeKube(t)
	kube.failStatefulSetCreate = true
	fleet := newTestFleet(t, kube)

	_, err := fleet.Do(memberContext("owner"), cloudruntime.Request{
		Method: http.MethodPost,
		Path:   "/api/v1/nodes",
		Body:   []byte(`{"instance_type":"t4g.medium"}`),
	})
	if err == nil {
		t.Fatal("expected create to fail when statefulset create fails")
	}
	var n int
	if qerr := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM cloud_node_token WHERE workspace_id = $1`, testWorkspaceID).Scan(&n); qerr != nil {
		t.Fatalf("count tokens: %v", qerr)
	}
	if n != 0 {
		t.Fatalf("token rows must be rolled back on k8s failure, found %d", n)
	}
}

func TestKubefleet_UnsupportedOpsReturn501(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	fleet := newTestFleet(t, newFakeKube(t))
	for _, path := range []string{"/api/v1/nodes/start", "/api/v1/nodes/stop", "/api/v1/nodes/reboot", "/api/v1/nodes/exec", "/api/v1/nodes/status"} {
		resp, _ := doJSON(t, fleet, memberContext("owner"), http.MethodPost, path, map[string]any{})
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("%s: status = %d, want 501", path, resp.StatusCode)
		}
	}
}
