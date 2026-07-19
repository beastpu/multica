package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// insertUpgradeTestAgent creates an agent bound to the shared test runtime and
// registers cleanup. Used to assert that an in-place skill upgrade preserves
// agent_skill bindings.
func insertUpgradeTestAgent(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("upgrade-test-agent-%d", time.Now().UnixNano())
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, permission_mode, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 'public_to', 1, $4)
		RETURNING id
	`, testWorkspaceID, name, testRuntimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID
}

// insertUpgradeTestSkillWithOrigin writes a skill whose config.origin.source_url
// points at sourceURL, so UpgradeSkill can re-fetch it.
func insertUpgradeTestSkillWithOrigin(t *testing.T, namePrefix, content, sourceURL string) string {
	t.Helper()
	name := namePrefix + "-" + t.Name()
	config := fmt.Sprintf(`{"origin":{"type":"clawhub","source_url":%q}}`, sourceURL)
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		RETURNING id
	`, testWorkspaceID, name, "fixture", content, config, testUserID).Scan(&id); err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, id)
	})
	return id
}

func TestUpgradeSkill_PreservesAgentBindingsAndUpdatesContent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()

	// The ClawHub mock always serves "# Imported\n" as the SKILL.md body.
	importURL := withMockClawHubImport(t, "upgrade-"+t.Name())
	skillID := insertUpgradeTestSkillWithOrigin(t, "upgrade-binding", "# stale content", importURL)

	agentID := insertUpgradeTestAgent(t)
	if _, err := testPool.Exec(ctx, `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`, agentID, skillID); err != nil {
		t.Fatalf("bind agent to skill: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/"+skillID+"/upgrade", nil)
	req = withURLParam(req, "id", skillID)
	testHandler.UpgradeSkill(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp SkillWithFilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.ID != skillID {
		t.Fatalf("skill id changed: got %s, want %s (in-place upgrade must reuse the row)", resp.ID, skillID)
	}
	if resp.Content != "# Imported\n" {
		t.Fatalf("content = %q, want the upgraded source body", resp.Content)
	}

	var bindings int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_skill WHERE agent_id = $1 AND skill_id = $2`,
		agentID, skillID,
	).Scan(&bindings); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if bindings != 1 {
		t.Fatalf("agent binding lost after upgrade: count = %d, want 1", bindings)
	}
}

func TestUpgradeSkill_AdminCanUpgradeSkillCreatedByAnother(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()

	// A skill created by a different member; the owner (testUserID) is not its
	// creator but must still be able to upgrade it in place.
	otherUserID := createRuntimeLocalSkillTestMember(t, "member")
	importURL := withMockClawHubImport(t, "upgrade-"+t.Name())
	name := "upgrade-admin-" + t.Name()
	config := fmt.Sprintf(`{"origin":{"type":"clawhub","source_url":%q}}`, importURL)
	var skillID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, $2, 'fixture', '# stale', $3::jsonb, $4)
		RETURNING id
	`, testWorkspaceID, name, config, otherUserID).Scan(&skillID); err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM skill WHERE id = $1`, skillID) })

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/"+skillID+"/upgrade", nil)
	req = withURLParam(req, "id", skillID)
	testHandler.UpgradeSkill(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("owner upgrade of another's skill: status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

func TestUpgradeSkill_NoOriginReturns400(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	// insertHandlerTestSkill writes config '{}', i.e. no recorded origin.
	skillID := insertHandlerTestSkill(t, "upgrade-no-origin", "# body")

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/"+skillID+"/upgrade", nil)
	req = withURLParam(req, "id", skillID)
	testHandler.UpgradeSkill(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}
