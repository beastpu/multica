package execenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateReviewDecision(t *testing.T) {
	for _, test := range []struct {
		name     string
		decision string
		reason   string
		wantErr  bool
	}{
		{name: "pass needs no reason", decision: "pass"},
		{name: "fail with a reason", decision: "fail", reason: "add the missing test"},
		{name: "blocked with a reason", decision: "blocked", reason: "the repo would not check out"},
		// A rejection nobody can act on costs a whole rework round.
		{name: "fail without a reason", decision: "fail", wantErr: true},
		{name: "blocked without a reason", decision: "blocked", wantErr: true},
		// The vocabulary a reviewer reached for on its own, three times. It is
		// refused here, where the refusal is immediate and legible, instead of
		// being inferred later from prose.
		{name: "approve is not a decision", decision: "approve", reason: "ok", wantErr: true},
		{name: "reject is not a decision", decision: "reject", reason: "no", wantErr: true},
		{name: "empty", decision: "", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateReviewDecision(test.decision, test.reason)
			if test.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestReviewDecisionRoundTrip(t *testing.T) {
	workDir := t.TempDir()
	markerPath := filepath.Join(workDir, TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeMarker := func(nodeInstanceID string) {
		t.Helper()
		body := `{"managed_by":"` + TaskContextMarkerManagedBy +
			`","workflow_node_instance_id":"` + nodeInstanceID + `"}`
		if err := os.WriteFile(markerPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeMarker("node-1")

	if got := ReadReviewDecision(workDir); got != nil {
		t.Fatalf("a decision was read before one was written: %+v", got)
	}

	if err := WriteReviewDecision(markerPath, ReviewDecision{
		Decision: "fail", Reason: "the button was removed, not wired",
		NodeInstanceID: "node-1",
	}); err != nil {
		t.Fatal(err)
	}
	got := ReadReviewDecision(workDir)
	if got == nil || got.Decision != "fail" ||
		got.Reason != "the button was removed, not wired" {
		t.Fatalf("unexpected decision: %+v", got)
	}

	// A rework reuses the work tree. The previous attempt's verdict must not
	// decide the new one.
	writeMarker("node-2")
	if stale := ReadReviewDecision(workDir); stale != nil {
		t.Fatalf("a verdict from another attempt was applied: %+v", stale)
	}
}

// A file that is not the daemon's, or that says something the contract does
// not accept, is treated as no verdict at all.
func TestReadReviewDecisionRejectsForeignAndInvalidFiles(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{name: "foreign file", body: `{"decision":"pass"}`},
		{name: "unknown decision", body: `{"managed_by":"` + TaskContextMarkerManagedBy + `","decision":"approve"}`},
		{name: "fail with no reason", body: `{"managed_by":"` + TaskContextMarkerManagedBy + `","decision":"fail"}`},
		{name: "not json", body: `verdict: approve`},
	} {
		t.Run(test.name, func(t *testing.T) {
			workDir := t.TempDir()
			path := filepath.Join(workDir, ReviewDecisionRelPath)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := ReadReviewDecision(workDir); got != nil {
				t.Fatalf("accepted %s: %+v", test.name, got)
			}
		})
	}
}
