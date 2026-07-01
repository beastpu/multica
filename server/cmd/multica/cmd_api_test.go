package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newAPITestCmd(serverURL string) *cobra.Command {
	cmd := &cobra.Command{Use: "api-test"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	_ = cmd.Flags().Set("server-url", serverURL)
	_ = cmd.Flags().Set("workspace-id", "ws-1")
	return cmd
}

func TestRunAPIGetUsesTaskScopedContext(t *testing.T) {
	t.Setenv("MULTICA_TOKEN", "mat_task_token")
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_TASK_ID", "task-1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/operations/agent-fixes/binding-1/p4-evidence" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer mat_task_token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "ws-1" {
			t.Fatalf("X-Workspace-ID = %q", got)
		}
		if got := r.Header.Get("X-Agent-ID"); got != "agent-1" {
			t.Fatalf("X-Agent-ID = %q", got)
		}
		if got := r.Header.Get("X-Task-ID"); got != "task-1" {
			t.Fatalf("X-Task-ID = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"binding_id": "binding-1",
			"status":     "done",
		})
	}))
	defer srv.Close()

	var out bytes.Buffer
	cmd := newAPITestCmd(srv.URL)
	err := runAPIGetWithWriter(cmd, []string{"/api/operations/agent-fixes/binding-1/p4-evidence"}, &out)
	if err != nil {
		t.Fatalf("runAPIGet: %v", err)
	}
	if !strings.Contains(out.String(), `"binding_id": "binding-1"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestRunAPIGetRejectsNonAPIPath(t *testing.T) {
	cmd := newAPITestCmd("http://127.0.0.1:0")
	err := runAPIGetWithWriter(cmd, []string{"https://example.com/api/test"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "/api/") {
		t.Fatalf("error = %q", err)
	}
}
