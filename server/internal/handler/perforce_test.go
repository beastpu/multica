package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/perforce"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeSwarmClient serves a fixed discovery page and id lookups.
type fakeSwarmClient struct {
	page []perforce.Review
	byID map[int64]perforce.Review
}

func (f *fakeSwarmClient) ListReviewsPage(_ context.Context, after int64, _ int) ([]perforce.Review, int64, error) {
	if after != 0 {
		return nil, 0, nil
	}
	return f.page, 0, nil
}

func (f *fakeSwarmClient) GetReview(_ context.Context, id int64) (perforce.Review, bool, error) {
	r, ok := f.byID[id]
	return r, ok, nil
}

func i64ptr(v int64) *int64 { return &v }

// enablePerforce opts the test workspace into Perforce (default is off) and
// resets the flag after the test so unrelated tests are unaffected.
func enablePerforce(ctx context.Context, t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(ctx,
		`UPDATE workspace SET settings = COALESCE(settings,'{}'::jsonb) || '{"perforce_enabled":true}'::jsonb WHERE id=$1`,
		testWorkspaceID); err != nil {
		t.Fatalf("enable perforce: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE workspace SET settings = settings - 'perforce_enabled' WHERE id=$1`, testWorkspaceID)
	})
}

// TestSyncPerforceWorkspace_CommittedReviewAdvancesIssue is the end-to-end
// happy path: a Swarm review whose description closes an issue and that has a
// committed changelist links the review and advances the issue to done.
func TestSyncPerforceWorkspace_CommittedReviewAdvancesIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()

	box, err := secretbox.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	prevBox, prevFactory := testHandler.PerforceBox, testHandler.NewSwarmClient
	testHandler.PerforceBox = box

	// Seed an issue the review will close.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "p4 sync test",
		"status": "in_progress",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: %d %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)

	review := perforce.Review{
		ID:          500123,
		State:       "approved",
		Title:       "fix crash",
		Description: "fix crash for " + created.Identifier + " (no closing keyword needed)",
		Author:      "alice",
		ShelvedCL:   i64ptr(500120),
		CommittedCL: i64ptr(500125),
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
		UpdatedAt:   time.Unix(1700000500, 0).UTC(),
		Updated:     1700000500,
	}
	testHandler.NewSwarmClient = func(perforce.Config) perforce.Client {
		return &fakeSwarmClient{
			page: []perforce.Review{review},
			byID: map[int64]perforce.Review{review.ID: review},
		}
	}

	sealed, err := box.Seal([]byte("pw"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	conn, err := testHandler.Queries.UpsertPerforceConnection(ctx, db.UpsertPerforceConnectionParams{
		WorkspaceID:          parseUUID(testWorkspaceID),
		SwarmUrl:             "http://swarm.test",
		SwarmUser:            "svc",
		SwarmTicketEncrypted: sealed,
	})
	if err != nil {
		t.Fatalf("UpsertPerforceConnection: %v", err)
	}

	t.Cleanup(func() {
		testHandler.PerforceBox = prevBox
		testHandler.NewSwarmClient = prevFactory
		testPool.Exec(ctx, `DELETE FROM issue_perforce_review WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM perforce_review WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM perforce_connection WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM activity_log WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	enablePerforce(ctx, t)

	res, err := testHandler.SyncPerforceWorkspace(ctx, conn)
	if err != nil {
		t.Fatalf("SyncPerforceWorkspace: %v", err)
	}
	if res.Discovered != 1 {
		t.Errorf("Discovered = %d, want 1", res.Discovered)
	}
	if res.Advanced != 1 {
		t.Errorf("Advanced = %d, want 1", res.Advanced)
	}

	rows, err := testHandler.Queries.ListReviewsByIssue(ctx, parseUUID(created.ID))
	if err != nil {
		t.Fatalf("ListReviewsByIssue: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("linked reviews = %d, want 1", len(rows))
	}
	if !rows[0].CommittedCl.Valid || rows[0].CommittedCl.Int32 != 500125 {
		t.Errorf("committed_cl = %+v, want 500125", rows[0].CommittedCl)
	}

	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, created.ID).Scan(&status); err != nil {
		t.Fatalf("read issue status: %v", err)
	}
	if status != "done" {
		t.Errorf("issue status = %q, want done", status)
	}
}

// TestSyncPerforceWorkspace_LinkedButNotCommittedDoesNotAdvance verifies the
// commit gate: a review that references the issue but is not yet committed links
// it but does not resolve it. (Perforce has no closing-keyword requirement — a
// committed reference closes the issue — so the only gate left is the commit.)
func TestSyncPerforceWorkspace_LinkedButNotCommittedDoesNotAdvance(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()

	box, err := secretbox.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	prevBox, prevFactory := testHandler.PerforceBox, testHandler.NewSwarmClient
	testHandler.PerforceBox = box

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "p4 mention test",
		"status": "in_progress",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: %d %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)

	review := perforce.Review{
		ID:          500200,
		State:       "approved",
		Title:       "touch " + created.Identifier,
		Description: "touch " + created.Identifier + " incidentally",
		ShelvedCL:   i64ptr(500199),
		// No CommittedCL: still under review, not submitted.
		CreatedAt: time.Unix(1700000000, 0).UTC(),
		UpdatedAt: time.Unix(1700000500, 0).UTC(),
		Updated:   1700000500,
	}
	testHandler.NewSwarmClient = func(perforce.Config) perforce.Client {
		return &fakeSwarmClient{page: []perforce.Review{review}, byID: map[int64]perforce.Review{review.ID: review}}
	}

	sealed, _ := box.Seal([]byte("pw"))
	conn, err := testHandler.Queries.UpsertPerforceConnection(ctx, db.UpsertPerforceConnectionParams{
		WorkspaceID:          parseUUID(testWorkspaceID),
		SwarmUrl:             "http://swarm.test",
		SwarmUser:            "svc",
		SwarmTicketEncrypted: sealed,
	})
	if err != nil {
		t.Fatalf("UpsertPerforceConnection: %v", err)
	}

	t.Cleanup(func() {
		testHandler.PerforceBox = prevBox
		testHandler.NewSwarmClient = prevFactory
		testPool.Exec(ctx, `DELETE FROM issue_perforce_review WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM perforce_review WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM perforce_connection WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	enablePerforce(ctx, t)

	if _, err := testHandler.SyncPerforceWorkspace(ctx, conn); err != nil {
		t.Fatalf("SyncPerforceWorkspace: %v", err)
	}

	rows, err := testHandler.Queries.ListReviewsByIssue(ctx, parseUUID(created.ID))
	if err != nil {
		t.Fatalf("ListReviewsByIssue: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("linked reviews = %d, want 1 (mention links)", len(rows))
	}

	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, created.ID).Scan(&status); err != nil {
		t.Fatalf("read issue status: %v", err)
	}
	if status != "in_progress" {
		t.Errorf("issue status = %q, want in_progress (review not yet committed)", status)
	}
}

// TestPerforceConnectionHTTP_TicketIsWriteOnly verifies the stored Swarm ticket
// is never returned to clients (only has_credential) and is preserved across a
// scope-only edit that omits the ticket.
func TestPerforceConnectionHTTP_TicketIsWriteOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	box, err := secretbox.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	prevBox := testHandler.PerforceBox
	testHandler.PerforceBox = box
	t.Cleanup(func() {
		testHandler.PerforceBox = prevBox
		testPool.Exec(ctx, `DELETE FROM perforce_connection WHERE workspace_id = $1`, testWorkspaceID)
	})

	const secret = "supersecret-ticket-value"
	save := func(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		req := newRequest(http.MethodPut, "/api/workspaces/"+testWorkspaceID+"/perforce/connection", body)
		req = withURLParam(req, "id", testWorkspaceID)
		req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, db.Member{Role: "admin"}))
		w := httptest.NewRecorder()
		testHandler.SavePerforceConnection(w, req)
		return w
	}

	// Create with a ticket.
	w := save(t, map[string]any{
		"swarm_url": "http://igame-swarm.lilithgame.com", "swarm_user": "svc",
		"ticket": secret, "depot_path": "//depot/main/...",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatalf("create response leaked the ticket: %s", w.Body.String())
	}

	// GET must report has_credential and never the ticket.
	getReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/perforce/connection", nil)
	getReq = withURLParam(getReq, "id", testWorkspaceID)
	getReq = getReq.WithContext(middleware.SetMemberContext(getReq.Context(), testWorkspaceID, db.Member{Role: "member"}))
	gw := httptest.NewRecorder()
	testHandler.GetPerforceConnection(gw, getReq)
	if gw.Code != http.StatusOK {
		t.Fatalf("get: %d %s", gw.Code, gw.Body.String())
	}
	if strings.Contains(gw.Body.String(), secret) {
		t.Fatalf("get response leaked the ticket: %s", gw.Body.String())
	}
	var body map[string]any
	json.Unmarshal(gw.Body.Bytes(), &body)
	conn, _ := body["connection"].(map[string]any)
	if conn == nil {
		t.Fatalf("expected a connection, got %v", body)
	}
	if hc, _ := conn["has_credential"].(bool); !hc {
		t.Errorf("has_credential = %v, want true", conn["has_credential"])
	}

	// Scope-only edit (no ticket) preserves the stored credential.
	w2 := save(t, map[string]any{
		"swarm_url": "http://igame-swarm.lilithgame.com", "swarm_user": "svc",
		"depot_path": "//depot/release/...",
	})
	if w2.Code != http.StatusOK {
		t.Fatalf("scope edit: %d %s", w2.Code, w2.Body.String())
	}
	var enc []byte
	if err := testPool.QueryRow(ctx, `SELECT swarm_ticket_encrypted FROM perforce_connection WHERE workspace_id = $1`, testWorkspaceID).Scan(&enc); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	plain, err := box.Open(enc)
	if err != nil || string(plain) != secret {
		t.Errorf("credential not preserved across scope-only edit (open err=%v)", err)
	}
}
