package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// workflowReviewCmd is how a Critic states its verdict.
//
// The verdict used to be the agent's final message, which the server parsed.
// That put a typed decision at the mercy of free text, and it kept going
// wrong in the one way parsing cannot survive: plausibly. A reviewer wrote
// `{"verdict":"approve"}` when it meant to reject, against a protocol that had
// shown it `result`/`reason` — read leniently, that would have approved a fix
// which deleted the button it was asked to wire up.
//
// A flag cannot drift into a different judgement. `--decision` is checked here,
// where the reviewer is, and the answer is recorded before the agent writes a
// word of prose.
var workflowReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Record this node's review verdict",
	Args:  cobra.NoArgs,
	RunE:  runWorkflowReview,
}

func init() {
	workflowCmd.AddCommand(workflowReviewCmd)
	workflowReviewCmd.Flags().String(
		"decision", "",
		"Verdict: "+strings.Join(execenv.ReviewDecisions, ", "),
	)
	workflowReviewCmd.Flags().String(
		"reason", "",
		"What must change (required unless the decision is pass)",
	)
}

func runWorkflowReview(cmd *cobra.Command, _ []string) error {
	decision := strings.TrimSpace(mustFlag(cmd, "decision"))
	reason := strings.TrimSpace(mustFlag(cmd, "reason"))
	if err := execenv.ValidateReviewDecision(decision, reason); err != nil {
		return err
	}

	markerPath := daemonTaskContextMarkerPath()
	if markerPath == "" {
		return fmt.Errorf(
			"this command runs inside a workflow review task; no daemon task was found here",
		)
	}
	instanceID, nodeKey := daemonTaskWorkflow()
	if instanceID == "" || nodeKey == "" {
		return errNotAWorkflowNode("")
	}

	if err := execenv.WriteReviewDecision(markerPath, execenv.ReviewDecision{
		Decision:       decision,
		Reason:         reason,
		NodeInstanceID: daemonTaskWorkflowNodeInstanceID(),
	}); err != nil {
		return err
	}

	fmt.Fprintf(
		cmd.OutOrStdout(),
		"Recorded %s for node %s. Finish the task; the server applies it on completion.\n",
		decision, nodeKey,
	)
	return nil
}
