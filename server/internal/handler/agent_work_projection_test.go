package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

// markIssueAsAgentWork stamps the reserved agent_work metadata marker the way
// the server-side projection does, bypassing the (guarded) user metadata API.
func markIssueAsAgentWork(t *testing.T, issueID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET metadata = '{"agent_work":{"kind":"p4_assessment"}}'::jsonb WHERE id = $1`,
		issueID,
	); err != nil {
		t.Fatalf("mark issue as agent_work: %v", err)
	}
}

// --- Guard carve-out: analysis tasks may narrate on their own projection issue ---

func TestAnalysisTaskCanCommentOnAgentWorkIssue(t *testing.T) {
	issueID := createTestIssue(t, "agent work narration target", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	markIssueAsAgentWork(t, issueID)
	agentID := createHandlerTestAgent(t, "Agent Work Narration Agent", nil)
	taskID := createAnalysisTaskForIssue(t, agentID, issueID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "process narration: verifying CL 12345",
	})
	req = withURLParam(req, "id", issueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for analysis task comment on agent_work issue, got %d: %s", w.Code, w.Body.String())
	}
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE issue_id = $1 AND author_type = 'agent' AND author_id = $2`,
		issueID, agentID,
	).Scan(&count); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 agent comment on agent_work issue, got %d", count)
	}
}

func TestAnalysisTaskStillCannotUpdateAgentWorkIssueStatus(t *testing.T) {
	issueID := createTestIssue(t, "agent work status guard", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	markIssueAsAgentWork(t, issueID)
	agentID := createHandlerTestAgent(t, "Agent Work Status Guard Agent", nil)
	taskID := createAnalysisTaskForIssue(t, agentID, issueID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{"status": "done"})
	req = withURLParam(req, "id", issueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403: status stays server-projected even on agent_work issues, got %d: %s", w.Code, w.Body.String())
	}
	assertIssueStatus(t, issueID, "todo")
}

// --- Reserved metadata key: agent_work is server-owned ---

func TestSetIssueMetadataRejectsReservedAgentWorkKey(t *testing.T) {
	issueID := createTestIssue(t, "reserved metadata set", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/issues/"+issueID+"/metadata/agent_work", map[string]any{"value": "spoof"})
	req = withURLParam(req, "id", issueID)
	req = withURLParam(req, "key", "agent_work")

	testHandler.SetIssueMetadataKey(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for reserved key set, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteIssueMetadataRejectsReservedAgentWorkKey(t *testing.T) {
	issueID := createTestIssue(t, "reserved metadata delete", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	markIssueAsAgentWork(t, issueID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodDelete, "/api/issues/"+issueID+"/metadata/agent_work", nil)
	req = withURLParam(req, "id", issueID)
	req = withURLParam(req, "key", "agent_work")

	testHandler.DeleteIssueMetadataKey(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for reserved key delete, got %d: %s", w.Code, w.Body.String())
	}
	var marked bool
	if err := testPool.QueryRow(context.Background(),
		`SELECT jsonb_exists(metadata, 'agent_work') FROM issue WHERE id = $1`, issueID,
	).Scan(&marked); err != nil {
		t.Fatalf("check metadata: %v", err)
	}
	if !marked {
		t.Fatalf("agent_work marker must survive the rejected delete")
	}
}

// --- Trigger creates the projection issue; force re-run creates a NEW one ---

// setupP4TriggerFixture wires a done Feishu binding AND a p4_assessment
// capability agent (local runtime) so Trigger's fail-closed gate passes.
// No assessment row exists yet.
func setupP4TriggerFixture(t *testing.T) (bindingID, issueID string) {
	t.Helper()
	agentID, _ := createLocalRuntimeAgent(t, "P4 Trigger Fixture Agent")
	configureP4Capability(t, agentID)
	return setupP4BindingFixture(t)
}

// setupP4BindingFixture wires the done Feishu binding only — no capability.
// Callers pick their opt-in path.
func setupP4BindingFixture(t *testing.T) (bindingID, issueID string) {
	t.Helper()
	ctx := context.Background()
	issueID = createTestIssue(t, "p4 trigger projection source", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	var integrationID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO feishu_project_integration (workspace_id, project_key, plugin_id, plugin_secret, created_by_id, status_mapping)
		VALUES ($1, 'P4TriggerFixture', 'plug', 'sec', $2, '{"Done":"done"}'::jsonb) RETURNING id
	`, testWorkspaceID, testUserID).Scan(&integrationID); err != nil {
		t.Fatalf("insert integration: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM feishu_project_integration WHERE id = $1`, integrationID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO feishu_project_issue_binding (
			workspace_id, integration_id, issue_id, project_key, work_item_type,
			work_item_id, external_identifier, external_url, external_status_label
		) VALUES ($1, $2, $3, 'P4TriggerFixture', 'issue', 'BUG-77', 'BUG-77', 'https://x.test/BUG-77', 'Done')
		RETURNING id
	`, testWorkspaceID, integrationID, issueID).Scan(&bindingID); err != nil {
		t.Fatalf("insert binding: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`, bindingID)
		testPool.Exec(ctx, `DELETE FROM feishu_project_issue_binding WHERE id = $1`, bindingID)
	})

	t.Cleanup(func() { cleanupAgentWorkArtifacts(t) })
	return bindingID, issueID
}

// cleanupAgentWorkArtifacts removes projection issues and the lazily created
// system project so tests stay hermetic.
func cleanupAgentWorkArtifacts(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	testPool.Exec(ctx, `DELETE FROM issue WHERE workspace_id = $1 AND jsonb_exists(metadata, 'agent_work')`, testWorkspaceID)
	testPool.Exec(ctx, `DELETE FROM project WHERE id IN (SELECT project_id FROM agent_work_project WHERE workspace_id = $1)`, testWorkspaceID)
	testPool.Exec(ctx, `DELETE FROM agent_work_project WHERE workspace_id = $1`, testWorkspaceID)
}

func triggerP4AssessmentRequest(t *testing.T, bindingID string, force bool) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/operations/assessments/trigger", map[string]any{
		"binding_id": bindingID,
		"force":      force,
	})
	testHandler.TriggerAgentFixP4Assessment(w, req)
	return w
}

func loadAssessmentIssueID(t *testing.T, bindingID string) string {
	t.Helper()
	var id *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT assessment_issue_id::text FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`,
		bindingID,
	).Scan(&id); err != nil {
		t.Fatalf("load assessment_issue_id: %v", err)
	}
	if id == nil {
		return ""
	}
	return *id
}

func TestTriggerP4AssessmentCreatesProjectionIssue(t *testing.T) {
	bindingID, _ := setupP4TriggerFixture(t)

	w := triggerP4AssessmentRequest(t, bindingID, false)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["created"] != true {
		t.Fatalf("expected created=true, got %v", resp)
	}

	projIssueID := loadAssessmentIssueID(t, bindingID)
	if projIssueID == "" {
		t.Fatalf("trigger must create a projection issue and write assessment_issue_id")
	}

	var status, kind, trigger, creatorType, title string
	var projectID *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, metadata->'agent_work'->>'kind', metadata->'agent_work'->>'trigger',
		       creator_type, title, project_id::text
		FROM issue WHERE id = $1`, projIssueID,
	).Scan(&status, &kind, &trigger, &creatorType, &title, &projectID); err != nil {
		t.Fatalf("load projection issue: %v", err)
	}
	if status != "todo" {
		t.Fatalf("projection issue status = %q, want todo", status)
	}
	if kind != service.AgentWorkKindP4Assessment {
		t.Fatalf("agent_work.kind = %q, want %q", kind, service.AgentWorkKindP4Assessment)
	}
	if trigger != "manual" {
		t.Fatalf("agent_work.trigger = %q, want manual", trigger)
	}
	if creatorType != "member" {
		t.Fatalf("creator_type = %q, want member (the triggering operator)", creatorType)
	}
	if title == "" {
		t.Fatalf("projection issue must have a title")
	}

	var mappedProject *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT project_id::text FROM agent_work_project WHERE workspace_id = $1 AND kind = $2`,
		testWorkspaceID, service.AgentWorkKindP4Assessment,
	).Scan(&mappedProject); err != nil {
		t.Fatalf("load agent_work_project mapping: %v", err)
	}
	if projectID == nil || mappedProject == nil || *projectID != *mappedProject {
		t.Fatalf("projection issue must live in the mapped system project (issue=%v mapping=%v)", projectID, mappedProject)
	}
}

func TestForceRerunCreatesNewProjectionIssueAndRepoints(t *testing.T) {
	bindingID, _ := setupP4TriggerFixture(t)

	if w := triggerP4AssessmentRequest(t, bindingID, false); w.Code != http.StatusOK {
		t.Fatalf("first trigger: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	firstIssueID := loadAssessmentIssueID(t, bindingID)
	if firstIssueID == "" {
		t.Fatalf("first trigger must create a projection issue")
	}
	// Move the queue row to a terminal state so force re-run is allowed, and
	// project the first issue done the way completion would.
	ctx := context.Background()
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_fix_p4_assessment SET assessment_status = 'completed' WHERE feishu_binding_id = $1`,
		bindingID,
	); err != nil {
		t.Fatalf("complete assessment row: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'done' WHERE id = $1`, firstIssueID); err != nil {
		t.Fatalf("project first issue done: %v", err)
	}

	if w := triggerP4AssessmentRequest(t, bindingID, true); w.Code != http.StatusOK {
		t.Fatalf("force trigger: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	secondIssueID := loadAssessmentIssueID(t, bindingID)
	if secondIssueID == "" || secondIssueID == firstIssueID {
		t.Fatalf("force re-run must create a NEW projection issue (first=%s second=%s)", firstIssueID, secondIssueID)
	}
	// The old run's issue keeps its terminal state — issue history IS the run history.
	assertIssueStatus(t, firstIssueID, "done")
	var trigger string
	if err := testPool.QueryRow(ctx,
		`SELECT metadata->'agent_work'->>'trigger' FROM issue WHERE id = $1`, secondIssueID,
	).Scan(&trigger); err != nil {
		t.Fatalf("load second issue trigger: %v", err)
	}
	if trigger != "force_rerun" {
		t.Fatalf("second issue agent_work.trigger = %q, want force_rerun", trigger)
	}
}
