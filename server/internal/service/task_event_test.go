package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestTaskEventPayloadIncludesFailureDetails(t *testing.T) {
	task := db.AgentTaskQueue{
		ID:            testUUID(0x11),
		AgentID:       testUUID(0x22),
		IssueID:       testUUID(0x33),
		ChatSessionID: testUUID(0x44),
		Status:        "failed",
		Error:         pgtype.Text{String: "claude exited with code 1", Valid: true},
		FailureReason: pgtype.Text{String: "agent_error.provider_server_error", Valid: true},
	}

	payload := taskEventPayload(task)

	if got := payload["task_id"]; got != util.UUIDToString(task.ID) {
		t.Fatalf("task_id = %v, want %s", got, util.UUIDToString(task.ID))
	}
	if got := payload["chat_session_id"]; got != util.UUIDToString(task.ChatSessionID) {
		t.Fatalf("chat_session_id = %v, want %s", got, util.UUIDToString(task.ChatSessionID))
	}
	if got := payload["error"]; got != "claude exited with code 1" {
		t.Fatalf("error = %v, want failure detail", got)
	}
	if got := payload["failure_reason"]; got != "agent_error.provider_server_error" {
		t.Fatalf("failure_reason = %v, want classified reason", got)
	}
}

func TestTaskEventPayloadDefaultsFailedReason(t *testing.T) {
	task := db.AgentTaskQueue{
		ID:      testUUID(0x55),
		AgentID: testUUID(0x66),
		IssueID: testUUID(0x77),
		Status:  "failed",
	}

	payload := taskEventPayload(task)

	if got := payload["failure_reason"]; got != "agent_error" {
		t.Fatalf("failure_reason = %v, want agent_error fallback", got)
	}
	if _, ok := payload["error"]; ok {
		t.Fatalf("error should be omitted when task error is empty: %v", payload)
	}
}
