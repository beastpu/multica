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

func TestRunAPIPostSendsJSONBodyAndTaskContext(t *testing.T) {
	t.Setenv("MULTICA_TOKEN", "mat_task_token")
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_TASK_ID", "task-1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/operations/agent-fixes/binding-1/p4-assessment/result" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Task-ID"); got != "task-1" {
			t.Fatalf("X-Task-ID = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["quality_prediction"] != "likely_wrong" {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed"})
	}))
	defer srv.Close()

	apiPostContentFile = "" // read from the provided reader (stdin stand-in)
	in := strings.NewReader(`{"quality_prediction":"likely_wrong"}`)
	var out bytes.Buffer
	cmd := newAPITestCmd(srv.URL)
	err := runAPIPostWithWriter(cmd, []string{"/api/operations/agent-fixes/binding-1/p4-assessment/result"}, in, &out)
	if err != nil {
		t.Fatalf("runAPIPost: %v", err)
	}
	if !strings.Contains(out.String(), `"status": "completed"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestRunAPIPostRejectsInvalidJSONBody(t *testing.T) {
	apiPostContentFile = ""
	cmd := newAPITestCmd("http://127.0.0.1:0")
	err := runAPIPostWithWriter(cmd, []string{"/api/x"}, strings.NewReader("not json"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("expected invalid-JSON error, got %v", err)
	}
}

func TestRunAPIPostRejectsNonAPIPath(t *testing.T) {
	apiPostContentFile = ""
	cmd := newAPITestCmd("http://127.0.0.1:0")
	err := runAPIPostWithWriter(cmd, []string{"https://example.com/api/test"}, strings.NewReader("{}"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "/api/") {
		t.Fatalf("expected path error, got %v", err)
	}
}
