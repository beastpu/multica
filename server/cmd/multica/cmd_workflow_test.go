package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// writeTaskMarker plants a daemon task marker in a temp tree and moves the
// process there, mirroring how a dispatched agent runs.
func writeTaskMarker(t *testing.T, managedBy, issueID string) {
	t.Helper()
	dir := t.TempDir()
	markerPath := filepath.Join(dir, execenv.TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		t.Fatalf("create marker dir: %v", err)
	}
	body, _ := json.Marshal(map[string]string{
		"managed_by": managedBy, "issue_id": issueID,
	})
	if err := os.WriteFile(markerPath, body, 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

// The zero-argument form is the whole point: an agent must not need an issue id
// copied into its prompt, because a copied identifier goes stale like any other
// copy.
func TestResolveWorkflowIssueIDFromDaemonMarker(t *testing.T) {
	writeTaskMarker(t, execenv.TaskContextMarkerManagedBy, "issue-123")
	got, err := resolveWorkflowIssueID(nil)
	if err != nil {
		t.Fatalf("resolveWorkflowIssueID() error = %v", err)
	}
	if got != "issue-123" {
		t.Errorf("resolveWorkflowIssueID() = %q, want %q", got, "issue-123")
	}
}

func TestResolveWorkflowIssueIDPrefersExplicitArgument(t *testing.T) {
	writeTaskMarker(t, execenv.TaskContextMarkerManagedBy, "issue-from-marker")
	got, err := resolveWorkflowIssueID([]string{"issue-explicit"})
	if err != nil {
		t.Fatalf("resolveWorkflowIssueID() error = %v", err)
	}
	if got != "issue-explicit" {
		t.Errorf("resolveWorkflowIssueID() = %q, want the explicit argument", got)
	}
}

// A marker written by something else is not a daemon signal. Trusting it would
// let an unrelated file steer the CLI at another workspace's issue.
func TestResolveWorkflowIssueIDIgnoresForeignMarker(t *testing.T) {
	writeTaskMarker(t, "someone-else", "issue-123")
	if _, err := resolveWorkflowIssueID(nil); err == nil {
		t.Fatal("resolveWorkflowIssueID() = nil error, want a rejection for a foreign marker")
	}
}

func TestCurrentNodePicksTheLiveAttempt(t *testing.T) {
	detail := workflowDetailEnvelope{Nodes: []workflowNodeSummary{
		{ID: "node-a1", NodeKey: "design", Attempt: 1, Status: "superseded"},
		{ID: "node-a2", NodeKey: "design", Attempt: 2, Status: "active"},
		{ID: "node-b1", NodeKey: "review", Attempt: 1, Status: "pending"},
	}}
	node, ok := currentNode(detail, "design")
	if !ok {
		t.Fatal("currentNode() not found")
	}
	if node.ID != "node-a2" {
		t.Errorf("currentNode() = %q, want the highest attempt", node.ID)
	}
	if _, ok := currentNode(detail, "missing"); ok {
		t.Error("currentNode() found a node that is not in the run")
	}
}

func newSubmitCommand() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("file", "", "")
	cmd.Flags().String("content", "", "")
	cmd.Flags().String("url", "", "")
	cmd.Flags().String("attachment-id", "", "")
	return cmd
}

func TestArtifactBodyFromFlags(t *testing.T) {
	tests := []struct {
		name    string
		flags   map[string]string
		wantKey string
		wantErr bool
	}{
		{name: "inline content", flags: map[string]string{"content": "Body."}, wantKey: "content"},
		{name: "link", flags: map[string]string{"url": "https://example.com/pr/1"}, wantKey: "url"},
		{name: "attachment", flags: map[string]string{"attachment-id": "att-1"}, wantKey: "attachment_id"},
		{name: "no carrier", flags: map[string]string{}, wantErr: true},
		{
			name:    "two carriers",
			flags:   map[string]string{"content": "Body.", "url": "https://example.com"},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := newSubmitCommand()
			for name, value := range test.flags {
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatalf("set %s: %v", name, err)
				}
			}
			body, err := artifactBodyFromFlags(cmd, "design_doc", "issue-1")
			if test.wantErr {
				if err == nil {
					t.Fatalf("artifactBodyFromFlags() = %v, want an error", body)
				}
				return
			}
			if err != nil {
				t.Fatalf("artifactBodyFromFlags() error = %v", err)
			}
			if body["artifact_key"] != "design_doc" {
				t.Errorf("artifact_key = %v, want design_doc", body["artifact_key"])
			}
			if _, exists := body[test.wantKey]; !exists {
				t.Errorf("body %v is missing %q", body, test.wantKey)
			}
		})
	}
}

func TestArtifactBodyFromFileFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "design.md")
	if err := os.WriteFile(path, []byte("# Design\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cmd := newSubmitCommand()
	if err := cmd.Flags().Set("file", path); err != nil {
		t.Fatalf("set file: %v", err)
	}
	body, err := artifactBodyFromFlags(cmd, "design_doc", "issue-1")
	if err != nil {
		t.Fatalf("artifactBodyFromFlags() error = %v", err)
	}
	if body["content"] != "# Design\n" {
		t.Errorf("content = %v, want the file body", body["content"])
	}
}
