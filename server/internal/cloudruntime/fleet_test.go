package cloudruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	testPool  *pgxpool.Pool
	testQ     *db.Queries
	testUser  string
	testWS    string
	testSlug  = "fleet-tests"
	testEmail = "fleet-adapter-test@multica.ai"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil || pool.Ping(ctx) != nil {
		fmt.Println("Skipping fleet adapter tests: database not reachable")
		os.Exit(0)
	}
	testPool, testQ = pool, db.New(pool)
	cleanup := func() {
		pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, testSlug)
		pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, testEmail)
	}
	cleanup()
	pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Fleet Test', $1) RETURNING id`, testEmail).Scan(&testUser)
	pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ('Fleet', $1, '', 'FLT') RETURNING id`, testSlug).Scan(&testWS)
	pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, testWS, testUser)
	code := m.Run()
	cleanup()
	pool.Close()
	os.Exit(code)
}

// fakeProvider records calls and lets tests control CountNodes / errors.
type fakeProvider struct {
	created   []NodeSpec
	deleted   []string
	count     int
	createErr error
}

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) CreateNode(_ context.Context, spec NodeSpec) (Node, error) {
	if p.createErr != nil {
		return Node{}, p.createErr
	}
	p.created = append(p.created, spec)
	return Node{ID: spec.Name, DisplayName: spec.DisplayName, Status: "launching"}, nil
}
func (p *fakeProvider) ListNodes(context.Context, string, string) ([]Node, error) { return nil, nil }
func (p *fakeProvider) DeleteNode(_ context.Context, _, _, name string) error {
	p.deleted = append(p.deleted, name)
	return nil
}
func (p *fakeProvider) CountNodes(context.Context, string, string) (int, error) { return p.count, nil }

func ctxFor(role string) context.Context {
	uid, _ := util.ParseUUID(testUser)
	wsID, _ := util.ParseUUID(testWS)
	return middleware.SetMemberContext(context.Background(), testWS, db.Member{
		WorkspaceID: wsID, UserID: uid, Role: role,
	})
}

func do(t *testing.T, f *Fleet, ctx context.Context, method string, body any) (*Response, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	if body == nil {
		raw = nil
	}
	resp, err := f.Do(ctx, Request{Method: method, Path: "/api/v1/nodes", Body: raw})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(resp.Body, &decoded)
	return resp, decoded
}

func TestFleet_CreateMintsTokenAndDeleteRevokes(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	fp := &fakeProvider{}
	f := NewFleet(FleetConfig{Provider: fp, Queries: testQ, MaxNodesPerWorkspace: 3})
	ctx := ctxFor("owner")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM cloud_node_token WHERE workspace_id = $1`, testWS)
	})

	resp, node := do(t, f, ctx, http.MethodPost, map[string]any{"name": "box", "instance_type": "t4g.medium"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, resp.Body)
	}
	nodeName := node["instance_id"].(string)
	if len(fp.created) != 1 || fp.created[0].Token == "" {
		t.Fatalf("provider not called with token: %+v", fp.created)
	}
	// Token row minted.
	var n int
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM cloud_node_token WHERE workspace_id = $1 AND node_name = $2`, testWS, nodeName).Scan(&n)
	if n != 1 {
		t.Fatalf("expected 1 token row, got %d", n)
	}
	// Delete revokes it.
	resp, _ = do(t, f, ctx, http.MethodDelete, map[string]any{"instance_id": nodeName})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if len(fp.deleted) != 1 {
		t.Fatalf("provider DeleteNode not called")
	}
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM cloud_node_token WHERE workspace_id = $1 AND node_name = $2`, testWS, nodeName).Scan(&n)
	if n != 0 {
		t.Fatalf("token not revoked, %d rows", n)
	}
}

func TestFleet_AuthAndQuota(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	// Non-admin → 403.
	f := NewFleet(FleetConfig{Provider: &fakeProvider{}, Queries: testQ, MaxNodesPerWorkspace: 3})
	if resp, _ := do(t, f, ctxFor("member"), http.MethodPost, map[string]any{"instance_type": "t4g.medium"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member create: %d, want 403", resp.StatusCode)
	}
	// At quota → 409.
	fq := NewFleet(FleetConfig{Provider: &fakeProvider{count: 3}, Queries: testQ, MaxNodesPerWorkspace: 3})
	if resp, _ := do(t, fq, ctxFor("owner"), http.MethodPost, map[string]any{"instance_type": "t4g.medium"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("over-quota create: %d, want 409", resp.StatusCode)
	}
}

func TestFleet_TokenRolledBackOnProviderFailure(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	fp := &fakeProvider{createErr: fmt.Errorf("boom")}
	f := NewFleet(FleetConfig{Provider: fp, Queries: testQ, MaxNodesPerWorkspace: 3})
	if _, err := f.Do(ctxFor("owner"), Request{Method: http.MethodPost, Path: "/api/v1/nodes", Body: []byte(`{"instance_type":"t4g.medium"}`)}); err == nil {
		t.Fatal("expected error")
	}
	var n int
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM cloud_node_token WHERE workspace_id = $1`, testWS).Scan(&n)
	if n != 0 {
		t.Fatalf("token not rolled back on provider failure, %d rows", n)
	}
}

func TestFleet_DispatchAndNil(t *testing.T) {
	f := NewFleet(FleetConfig{Provider: &fakeProvider{}, Queries: testQ})
	resp, _ := f.Do(context.Background(), Request{Method: http.MethodGet, Path: "/healthz"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}
	resp, _ = f.Do(context.Background(), Request{Method: http.MethodPost, Path: "/api/v1/nodes/exec"})
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("exec: %d, want 501", resp.StatusCode)
	}
	// NewFleet returns nil without a provider.
	if NewFleet(FleetConfig{Queries: testQ}) != nil {
		t.Fatal("expected nil fleet without provider")
	}
}
