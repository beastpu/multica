package execenv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ReviewDecisionRelPath is where a Critic records its verdict, beside the task
// context marker the CLI already discovers by walking up from the working
// directory.
//
// The verdict travels as a file rather than as the agent's final text because
// text is what kept losing it. A reviewer that reaches a sound judgement can
// still express it in a shape the protocol does not accept — WTE-14841's Critic
// wrote `verdict`/`reason` three times against a protocol that showed it
// `result`/`reason`, and once tried fourteen request bodies against an endpoint
// that refuses agents. A `--decision` flag cannot be misspelled into something
// that parses as a different judgement: it is checked by the command that
// accepts it, at the moment the reviewer decides.
const ReviewDecisionRelPath = ".multica/review_decision.json"

// ReviewDecisions are the verdicts a Critic may record. They are the same set
// the verdict endpoint and the workflow_node_verdict row accept, so a decision
// never has to be translated on its way to the database.
var ReviewDecisions = []string{"pass", "fail", "blocked"}

// ReviewDecision is one Critic's verdict, written by `multica workflow review`
// and read by the daemon when the review task ends.
type ReviewDecision struct {
	ManagedBy string `json:"managed_by"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason,omitempty"`
	// NodeInstanceID guards against a verdict written under one attempt being
	// read by the next. A rework opens a new node instance in the same work
	// tree, and a stale file would otherwise decide it.
	NodeInstanceID string `json:"node_instance_id,omitempty"`
}

// ValidateReviewDecision reports why a decision cannot be recorded, or nil.
func ValidateReviewDecision(decision, reason string) error {
	decision = strings.TrimSpace(decision)
	if !slices.Contains(ReviewDecisions, decision) {
		return fmt.Errorf(
			"decision must be one of %s", strings.Join(ReviewDecisions, ", "),
		)
	}
	// A rejection the worker cannot act on sends the node round again for
	// nothing, so the reason is required exactly where it is needed.
	if decision != "pass" && strings.TrimSpace(reason) == "" {
		return fmt.Errorf("a %s decision needs a reason the worker can act on", decision)
	}
	return nil
}

// WriteReviewDecision records the verdict beside the marker at markerPath.
func WriteReviewDecision(markerPath string, decision ReviewDecision) error {
	if err := ValidateReviewDecision(decision.Decision, decision.Reason); err != nil {
		return err
	}
	decision.ManagedBy = TaskContextMarkerManagedBy
	decision.Decision = strings.TrimSpace(decision.Decision)
	decision.Reason = strings.TrimSpace(decision.Reason)
	path := filepath.Join(filepath.Dir(markerPath), filepath.Base(ReviewDecisionRelPath))
	payload, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode review decision: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("write review decision: %w", err)
	}
	return nil
}

// ReadReviewDecision returns the verdict recorded in this work tree, or nil
// when none was recorded.
//
// The attempt it belongs to is read from the marker in the same tree rather
// than passed in, so there is one answer to "which attempt is this" and the
// caller cannot supply a different one. A file naming another attempt is
// ignored rather than repaired: the only thing worse than a missing verdict is
// a confident wrong one.
func ReadReviewDecision(workDir string) *ReviewDecision {
	return readReviewDecision(workDir, markerNodeInstanceID(workDir))
}

// markerNodeInstanceID reads the node attempt from the task marker. An absent
// or unreadable marker yields "", which disables the check rather than
// discarding a real verdict.
func markerNodeInstanceID(workDir string) string {
	data, err := os.ReadFile(filepath.Join(workDir, TaskContextMarkerRelPath))
	if err != nil {
		return ""
	}
	var marker struct {
		ManagedBy              string `json:"managed_by"`
		WorkflowNodeInstanceID string `json:"workflow_node_instance_id"`
	}
	if json.Unmarshal(data, &marker) != nil ||
		marker.ManagedBy != TaskContextMarkerManagedBy {
		return ""
	}
	return strings.TrimSpace(marker.WorkflowNodeInstanceID)
}

func readReviewDecision(workDir, nodeInstanceID string) *ReviewDecision {
	path := filepath.Join(workDir, ReviewDecisionRelPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var decision ReviewDecision
	if json.Unmarshal(data, &decision) != nil ||
		decision.ManagedBy != TaskContextMarkerManagedBy {
		return nil
	}
	if ValidateReviewDecision(decision.Decision, decision.Reason) != nil {
		return nil
	}
	if nodeInstanceID != "" && decision.NodeInstanceID != "" &&
		decision.NodeInstanceID != nodeInstanceID {
		return nil
	}
	return &decision
}
