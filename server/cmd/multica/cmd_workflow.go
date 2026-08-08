package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// Workflow commands are the agent's navigation surface. They exist so an agent
// running a node can answer "where am I, what came before, and where do I put
// what I produced" without any identifier being copied into its prompt — a copy
// would go stale exactly like a copied requirement does.

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Inspect and contribute to the workflow you are running in",
}

var workflowCurrentCmd = &cobra.Command{
	Use:   "current [issue-id]",
	Short: "Show the workflow run and node for the current (or given) issue",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkflowCurrent,
}

var workflowArtifactsCmd = &cobra.Command{
	Use:   "artifacts [issue-id]",
	Short: "List the artifacts produced so far in this workflow run",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkflowArtifacts,
}

var workflowArtifactCmd = &cobra.Command{
	Use:   "artifact",
	Short: "Work with a single artifact",
}

var workflowArtifactGetCmd = &cobra.Command{
	Use:   "get <artifact-id> [issue-id]",
	Short: "Print one artifact's body",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runWorkflowArtifactGet,
}

var workflowUpstreamCmd = &cobra.Command{
	Use:   "upstream [issue-id]",
	Short: "Show the handoff summary and artifact index of each direct predecessor",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkflowUpstream,
}

var workflowSubmitCmd = &cobra.Command{
	Use:   "submit [issue-id]",
	Short: "Submit an artifact for the current node",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkflowSubmit,
}

func init() {
	workflowCmd.AddCommand(workflowCurrentCmd)
	workflowCmd.AddCommand(workflowUpstreamCmd)
	workflowCmd.AddCommand(workflowArtifactsCmd)
	workflowCmd.AddCommand(workflowArtifactCmd)
	workflowArtifactCmd.AddCommand(workflowArtifactGetCmd)
	workflowCmd.AddCommand(workflowSubmitCmd)

	workflowCurrentCmd.Flags().String("output", "table", "Output format: table or json")
	workflowArtifactsCmd.Flags().String("output", "table", "Output format: table or json")
	workflowUpstreamCmd.Flags().String("output", "table", "Output format: table or json")
	workflowArtifactGetCmd.Flags().String("output", "text", "Output format: text or json")

	workflowSubmitCmd.Flags().String("summary", "", "Handoff summary for the next node")
	workflowSubmitCmd.Flags().StringArray("set", nil,
		"One structured output field as key=value (repeatable)")
	workflowSubmitCmd.Flags().String("json", "",
		"All structured output fields as one JSON object")
	workflowSubmitCmd.Flags().String("artifact", "", "Artifact key declared by the node")
	workflowSubmitCmd.Flags().String("file", "", "Read the document body from this file")
	workflowSubmitCmd.Flags().String("content", "", "Document body given inline")
	workflowSubmitCmd.Flags().String("url", "", "External address for a link artifact")
	workflowSubmitCmd.Flags().String("attachment-id", "", "Attachment id for an attachment artifact")
	workflowSubmitCmd.Flags().String("output", "table", "Output format: table or json")
}

// daemonTaskIssueID reads the issue this process was dispatched for from the
// daemon's task marker.
//
// The marker is used rather than an environment variable because a sandboxed
// agent can lose every MULTICA_* variable before it reaches the CLI; the marker
// survives because it is discovered by walking up from the working directory.
// That is also why these commands take no identifier in the common case.
func daemonTaskIssueID() string {
	markerPath := daemonTaskContextMarkerPath()
	if markerPath == "" {
		return ""
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return ""
	}
	var marker struct {
		ManagedBy string `json:"managed_by"`
		IssueID   string `json:"issue_id"`
	}
	if json.Unmarshal(data, &marker) != nil ||
		marker.ManagedBy != execenv.TaskContextMarkerManagedBy {
		return ""
	}
	return strings.TrimSpace(marker.IssueID)
}

// daemonTaskWorkflow reads the node this task executes straight from the
// daemon marker. A node with issue_policy: none has no issue to resolve
// through, and the issue was only ever a carrier anyway — the node is what the
// command acts on.
func daemonTaskWorkflow() (instanceID, nodeKey string) {
	markerPath := daemonTaskContextMarkerPath()
	if markerPath == "" {
		return "", ""
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return "", ""
	}
	var marker struct {
		ManagedBy          string `json:"managed_by"`
		WorkflowInstanceID string `json:"workflow_instance_id"`
		WorkflowNodeKey    string `json:"workflow_node_key"`
	}
	if json.Unmarshal(data, &marker) != nil ||
		marker.ManagedBy != execenv.TaskContextMarkerManagedBy {
		return "", ""
	}
	return strings.TrimSpace(marker.WorkflowInstanceID),
		strings.TrimSpace(marker.WorkflowNodeKey)
}

// daemonTaskWorkflowNodeInstanceID reads which attempt of the node this task
// belongs to. A rework opens a new node instance in the same work tree, so a
// verdict has to name the attempt it judged or the next one inherits it.
func daemonTaskWorkflowNodeInstanceID() string {
	markerPath := daemonTaskContextMarkerPath()
	if markerPath == "" {
		return ""
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return ""
	}
	var marker struct {
		ManagedBy              string `json:"managed_by"`
		WorkflowNodeInstanceID string `json:"workflow_node_instance_id"`
	}
	if json.Unmarshal(data, &marker) != nil ||
		marker.ManagedBy != execenv.TaskContextMarkerManagedBy {
		return ""
	}
	return strings.TrimSpace(marker.WorkflowNodeInstanceID)
}

// errNotAWorkflowNode explains the failure in terms of what the caller
// actually has. With no issue and no node in the marker, "issue  is not a
// workflow node issue" named an empty string as the culprit.
func errNotAWorkflowNode(issueID string) error {
	if strings.TrimSpace(issueID) == "" {
		return fmt.Errorf(
			"this task carries no workflow node; run inside a node task, " +
				"or pass the node's issue id explicitly",
		)
	}
	return fmt.Errorf("issue %s is not a workflow node issue", issueID)
}

func resolveWorkflowIssueID(args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return strings.TrimSpace(args[0]), nil
	}
	if issueID := daemonTaskIssueID(); issueID != "" {
		return issueID, nil
	}
	// A direct-execution node has no issue, and that is not an error: the
	// marker names the node itself, and resolveWorkflowContext prefers it.
	// Returning empty lets the node-first path run instead of refusing here.
	if instanceID, _ := daemonTaskWorkflow(); instanceID != "" {
		return "", nil
	}
	return "", fmt.Errorf(
		"no issue id given and no daemon task context found; pass an issue id explicitly",
	)
}

type workflowIssueMetadataEnvelope struct {
	Metadata struct {
		Workflow struct {
			InstanceID string `json:"instance_id"`
			NodeKey    string `json:"node_key"`
			HostIssue  string `json:"host_issue"`
		} `json:"workflow"`
	} `json:"metadata"`
}

// workflowArtifactRequirement is what a node declares it owes. An agent needs
// these keys to submit anything at all — the submit endpoint only accepts a key
// the node declared — so they have to be discoverable, not guessed.
type workflowArtifactRequirement struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
}

type workflowNodeDefinition struct {
	Artifacts []workflowArtifactRequirement `json:"artifacts"`
}

type workflowNodeSummary struct {
	ID         string                 `json:"id"`
	NodeKey    string                 `json:"node_key"`
	NodeKind   string                 `json:"node_kind"`
	Name       string                 `json:"name_snapshot"`
	Status     string                 `json:"status"`
	Attempt    int32                  `json:"attempt"`
	Definition workflowNodeDefinition `json:"definition"`
}

type workflowInstanceSummary struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	HostIssueID string `json:"host_issue_id"`
}

type workflowDetailEnvelope struct {
	Instance workflowInstanceSummary `json:"instance"`
	Nodes    []workflowNodeSummary   `json:"nodes"`
}

// resolveWorkflowContext finds the run an issue belongs to, from either end.
//
// A node child issue carries its run in issue.metadata, stamped when the issue
// was materialized. A host issue carries nothing, so it is resolved through its
// own workflow endpoint. Trying the metadata first means the common case — an
// agent working a node — costs one lookup and never guesses.
func resolveWorkflowContext(
	client *cli.APIClient,
	issueID string,
) (workflowDetailEnvelope, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The marker names the node directly when the daemon put one there. Going
	// through the issue first meant an issue-less node could not be addressed
	// at all, however plainly it was executing.
	if instanceID, nodeKey := daemonTaskWorkflow(); instanceID != "" && nodeKey != "" {
		var detail workflowDetailEnvelope
		if err := client.GetJSON(
			ctx, "/api/workflow-instances/"+instanceID, &detail,
		); err != nil {
			return workflowDetailEnvelope{}, "", fmt.Errorf("get workflow: %w", err)
		}
		return detail, nodeKey, nil
	}

	if issueID == "" {
		return workflowDetailEnvelope{}, "", fmt.Errorf(
			"no workflow node in the daemon task context and no issue id given",
		)
	}

	var envelope workflowIssueMetadataEnvelope
	if err := client.GetJSON(ctx, "/api/issues/"+issueID, &envelope); err != nil {
		return workflowDetailEnvelope{}, "", fmt.Errorf("get issue: %w", err)
	}
	nodeKey := envelope.Metadata.Workflow.NodeKey
	path := "/api/issues/" + issueID + "/workflow"
	if instanceID := envelope.Metadata.Workflow.InstanceID; instanceID != "" {
		path = "/api/workflow-instances/" + instanceID
	}
	var detail workflowDetailEnvelope
	if err := client.GetJSON(ctx, path, &detail); err != nil {
		return workflowDetailEnvelope{}, "", fmt.Errorf("get workflow: %w", err)
	}
	return detail, nodeKey, nil
}

// currentNode picks the node this issue belongs to. Attempts are ordered, so
// the highest one is the live attempt; an earlier attempt is superseded work
// that must not be written to.
func currentNode(detail workflowDetailEnvelope, nodeKey string) (workflowNodeSummary, bool) {
	var selected workflowNodeSummary
	found := false
	for _, node := range detail.Nodes {
		if node.NodeKey != nodeKey {
			continue
		}
		if !found || node.Attempt > selected.Attempt {
			selected = node
			found = true
		}
	}
	return selected, found
}

func runWorkflowCurrent(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	issueID, err := resolveWorkflowIssueID(args)
	if err != nil {
		return err
	}
	detail, nodeKey, err := resolveWorkflowContext(client, issueID)
	if err != nil {
		return err
	}
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, map[string]any{
			"workflow_instance": detail.Instance,
			"current_node_key":  nodeKey,
			"nodes":             detail.Nodes,
		})
	}
	fmt.Fprintf(os.Stdout, "workflow run %s (%s)\n", detail.Instance.ID, detail.Instance.Status)
	if nodeKey != "" {
		if node, ok := currentNode(detail, nodeKey); ok {
			fmt.Fprintf(os.Stdout, "current node: %s (%s, attempt %d)\n",
				node.Name, node.Status, node.Attempt)
		}
	}
	if node, ok := currentNode(detail, nodeKey); ok && len(node.Definition.Artifacts) > 0 {
		delivered, _ := fetchWorkflowArtifacts(client, detail.Instance.ID)
		done := map[string]string{}
		for _, artifact := range delivered {
			if artifact.ArtifactKey != "" {
				done[artifact.ArtifactKey] = artifact.ReviewStatus
			}
		}
		fmt.Fprintln(os.Stdout, "\nartifacts this node owes:")
		for _, requirement := range node.Definition.Artifacts {
			state := "not submitted"
			if status, exists := done[requirement.Key]; exists {
				state = status
			}
			necessity := "optional"
			if requirement.Required {
				necessity = "required"
			}
			kind := requirement.Kind
			if kind == "" {
				kind = "document"
			}
			fmt.Fprintf(os.Stdout, "  %-20s %-10s %-9s %-14s %s\n",
				requirement.Key, kind, necessity, state, requirement.Name)
		}
		fmt.Fprintln(os.Stdout,
			"\nSubmit one with: multica workflow submit --artifact <key> --file <path>")
	}
	fmt.Fprintln(os.Stdout, "\nnodes:")
	for _, node := range detail.Nodes {
		marker := " "
		if node.NodeKey == nodeKey {
			marker = "*"
		}
		fmt.Fprintf(os.Stdout, "  %s %-20s %-12s %s\n", marker, node.NodeKey, node.Status, node.Name)
	}
	return nil
}

type workflowArtifactSummary struct {
	ID           string `json:"id"`
	ArtifactKey  string `json:"artifact_key"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Content      string `json:"content"`
	URL          string `json:"url"`
	AttachmentID string `json:"attachment_id"`
	ReviewStatus string `json:"review_status"`
}

type workflowArtifactsEnvelope struct {
	Artifacts []workflowArtifactSummary `json:"artifacts"`
}

func fetchWorkflowArtifacts(
	client *cli.APIClient,
	instanceID string,
) ([]workflowArtifactSummary, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var envelope workflowArtifactsEnvelope
	if err := client.GetJSON(
		ctx, "/api/workflow-instances/"+instanceID+"/artifacts", &envelope,
	); err != nil {
		return nil, fmt.Errorf("list workflow artifacts: %w", err)
	}
	return envelope.Artifacts, nil
}

func runWorkflowArtifacts(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	issueID, err := resolveWorkflowIssueID(args)
	if err != nil {
		return err
	}
	detail, _, err := resolveWorkflowContext(client, issueID)
	if err != nil {
		return err
	}
	artifacts, err := fetchWorkflowArtifacts(client, detail.Instance.ID)
	if err != nil {
		return err
	}
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, workflowArtifactsEnvelope{Artifacts: artifacts})
	}
	if len(artifacts) == 0 {
		fmt.Fprintln(os.Stdout, "no artifacts yet")
		return nil
	}
	// The listing is an index, not the bodies. Reading one is a separate,
	// deliberate step so a long run does not drown the caller in prose it did
	// not ask for.
	for _, artifact := range artifacts {
		fmt.Fprintf(os.Stdout, "%s  [%s/%s]  %s\n",
			artifact.ID, artifact.Kind, artifact.ReviewStatus, artifact.Name)
		if artifact.Description != "" {
			fmt.Fprintf(os.Stdout, "    %s\n", artifact.Description)
		}
		if artifact.URL != "" {
			fmt.Fprintf(os.Stdout, "    %s\n", artifact.URL)
		}
	}
	fmt.Fprintln(os.Stdout, "\nRead one with: multica workflow artifact get <artifact-id>")
	return nil
}

func runWorkflowArtifactGet(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	artifactID := strings.TrimSpace(args[0])
	issueID, err := resolveWorkflowIssueID(args[1:])
	if err != nil {
		return err
	}
	detail, _, err := resolveWorkflowContext(client, issueID)
	if err != nil {
		return err
	}
	artifacts, err := fetchWorkflowArtifacts(client, detail.Instance.ID)
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if artifact.ID != artifactID {
			continue
		}
		if output, _ := cmd.Flags().GetString("output"); output == "json" {
			return cli.PrintJSON(os.Stdout, artifact)
		}
		switch artifact.Kind {
		case "link":
			fmt.Fprintln(os.Stdout, artifact.URL)
		case "attachment":
			fmt.Fprintf(os.Stdout,
				"attachment %s — download it with: multica attachment download %s\n",
				artifact.AttachmentID, artifact.AttachmentID)
		default:
			fmt.Fprintln(os.Stdout, artifact.Content)
		}
		return nil
	}
	return fmt.Errorf("artifact %s is not part of this workflow run", artifactID)
}

// artifactBodyFromFlags reads exactly one carrier off the flags. Rejecting a
// second one here rather than letting the server pick keeps the failure at the
// point the caller can see what it typed.
func artifactBodyFromFlags(
	cmd *cobra.Command,
	artifactKey string,
	issueID string,
) (map[string]any, error) {
	file, _ := cmd.Flags().GetString("file")
	content, _ := cmd.Flags().GetString("content")
	url, _ := cmd.Flags().GetString("url")
	attachmentID, _ := cmd.Flags().GetString("attachment-id")

	carriers := 0
	for _, value := range []string{file, content, url, attachmentID} {
		if strings.TrimSpace(value) != "" {
			carriers++
		}
	}
	if carriers == 0 {
		return nil, fmt.Errorf("one of --file, --content, --url or --attachment-id is required")
	}
	if carriers > 1 {
		return nil, fmt.Errorf("give only one of --file, --content, --url or --attachment-id")
	}

	// The issue travels with the submission so the server can leave a trace on
	// it: delivering and making the delivery visible to people are one action,
	// not two.
	body := map[string]any{"artifact_key": artifactKey, "issue_id": issueID}
	switch {
	case strings.TrimSpace(file) != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read artifact file: %w", err)
		}
		body["content"] = string(data)
	case strings.TrimSpace(content) != "":
		body["content"] = content
	case strings.TrimSpace(url) != "":
		body["url"] = url
	default:
		body["attachment_id"] = attachmentID
	}
	return body, nil
}

type workflowUpstreamEntry struct {
	NodeKey   string                    `json:"node_key"`
	Name      string                    `json:"name"`
	Status    string                    `json:"status"`
	Summary   string                    `json:"summary"`
	Artifacts []workflowArtifactSummary `json:"artifacts"`
}

func runWorkflowUpstream(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	issueID, err := resolveWorkflowIssueID(args)
	if err != nil {
		return err
	}
	detail, nodeKey, err := resolveWorkflowContext(client, issueID)
	if err != nil {
		return err
	}
	if nodeKey == "" {
		return errNotAWorkflowNode(issueID)
	}
	node, ok := currentNode(detail, nodeKey)
	if !ok {
		return fmt.Errorf("workflow node %q is not part of run %s", nodeKey, detail.Instance.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var response struct {
		Upstream []workflowUpstreamEntry `json:"upstream"`
	}
	if err := client.GetJSON(
		ctx, "/api/workflow-node-instances/"+node.ID+"/upstream", &response,
	); err != nil {
		return fmt.Errorf("get upstream: %w", err)
	}
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, response)
	}
	if len(response.Upstream) == 0 {
		fmt.Fprintln(os.Stdout, "no upstream nodes; this is the first activity in the run")
		return nil
	}
	for _, entry := range response.Upstream {
		fmt.Fprintf(os.Stdout, "%s (%s)\n", entry.Name, entry.Status)
		if entry.Summary != "" {
			fmt.Fprintf(os.Stdout, "  handoff: %s\n", entry.Summary)
		} else {
			fmt.Fprintln(os.Stdout, "  handoff: (not submitted)")
		}
		for _, artifact := range entry.Artifacts {
			fmt.Fprintf(os.Stdout, "  artifact %s  [%s/%s]  %s\n",
				artifact.ID, artifact.Kind, artifact.ReviewStatus, artifact.Name)
		}
	}
	fmt.Fprintln(os.Stdout, "\nRead a body with: multica workflow artifact get <artifact-id>")
	return nil
}

func runWorkflowSubmit(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	issueID, err := resolveWorkflowIssueID(args)
	if err != nil {
		return err
	}
	artifactKey := strings.TrimSpace(mustFlag(cmd, "artifact"))
	summary := strings.TrimSpace(mustFlag(cmd, "summary"))
	outputs, err := workflowOutputsFromFlags(cmd)
	if err != nil {
		return err
	}
	if artifactKey == "" && summary == "" && len(outputs) == 0 {
		return fmt.Errorf("give --summary, --set/--json output fields, --artifact, or a combination")
	}
	var body map[string]any
	if artifactKey != "" {
		var err error
		body, err = artifactBodyFromFlags(cmd, artifactKey, issueID)
		if err != nil {
			return err
		}
	}
	detail, nodeKey, err := resolveWorkflowContext(client, issueID)
	if err != nil {
		return err
	}
	if nodeKey == "" {
		return errNotAWorkflowNode(issueID)
	}
	node, ok := currentNode(detail, nodeKey)
	if !ok {
		return fmt.Errorf("workflow node %q is not part of run %s", nodeKey, detail.Instance.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The artifact goes first. Its absence is what blocks completion, and the
	// summary is meant to point at it — a summary landing first would name
	// something that is not there yet.
	results := map[string]any{}
	if body != nil {
		var response struct {
			Artifact workflowArtifactSummary `json:"artifact"`
			Replaced bool                    `json:"replaced"`
		}
		if err := client.PostJSON(
			ctx, "/api/workflow-node-instances/"+node.ID+"/artifacts", body, &response,
		); err != nil {
			return fmt.Errorf("submit artifact: %w", err)
		}
		results["artifact"] = response.Artifact
		results["replaced"] = response.Replaced
	}
	if summary != "" || len(outputs) > 0 {
		var response struct {
			Submission struct {
				ID      string `json:"id"`
				Summary string `json:"summary"`
			} `json:"submission"`
		}
		// Map marshaling is key-sorted, so the digest is stable and a retried
		// submit with the same summary and fields lands as the same handoff.
		digestSource, _ := json.Marshal(map[string]any{"summary": summary, "outputs": outputs})
		request := map[string]any{
			"summary":         summary,
			"idempotency_key": "handoff-" + node.ID + "-" + shortDigest(string(digestSource)),
		}
		if len(outputs) > 0 {
			request["payload"] = outputs
		}
		if err := client.PostJSON(
			ctx, "/api/workflow-node-instances/"+node.ID+"/submissions",
			request, &response,
		); err != nil {
			return workflowSubmitOutputsError(err)
		}
		results["submission"] = response.Submission
	}
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, results)
	}
	if body != nil {
		verb := "submitted"
		if replaced, _ := results["replaced"].(bool); replaced {
			verb = "replaced"
		}
		if artifact, ok := results["artifact"].(workflowArtifactSummary); ok {
			fmt.Fprintf(os.Stdout, "%s %s (%s)\n", verb, artifact.Name, artifact.ID)
		}
	}
	if summary != "" || len(outputs) > 0 {
		fmt.Fprintln(os.Stdout, "handoff submitted")
	}
	return nil
}

// workflowOutputsFromFlags merges --json and --set into one field map. --set
// wins on overlap so a mostly-JSON submission can still patch one field.
// Values stay strings here; the server coerces them against the node's
// declared field types, which the CLI has no way to know.
func workflowOutputsFromFlags(cmd *cobra.Command) (map[string]any, error) {
	outputs := map[string]any{}
	if raw := strings.TrimSpace(mustFlag(cmd, "json")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &outputs); err != nil {
			return nil, fmt.Errorf("--json must be one JSON object: %w", err)
		}
	}
	pairs, _ := cmd.Flags().GetStringArray("set")
	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, fmt.Errorf("--set expects key=value, got %q", pair)
		}
		outputs[key] = value
	}
	if len(outputs) == 0 {
		return nil, nil
	}
	return outputs, nil
}

// workflowSubmitOutputsError turns the server's structured field errors into
// lines an agent can act on directly, instead of an opaque HTTP 400.
func workflowSubmitOutputsError(err error) error {
	var httpErr *cli.HTTPError
	if !errors.As(err, &httpErr) {
		return fmt.Errorf("submit handoff: %w", err)
	}
	var body struct {
		Error  string `json:"error"`
		Fields []struct {
			Key      string   `json:"key"`
			Problem  string   `json:"problem"`
			Got      string   `json:"got"`
			Expected []string `json:"expected"`
		} `json:"fields"`
	}
	if json.Unmarshal([]byte(httpErr.Body), &body) != nil ||
		body.Error != "output_validation_failed" {
		return fmt.Errorf("submit handoff: %w", err)
	}
	fmt.Fprintln(os.Stderr, "output validation failed:")
	for _, field := range body.Fields {
		line := "  - " + field.Key + ": " + field.Problem
		if field.Got != "" {
			line += fmt.Sprintf(" (got %q)", field.Got)
		}
		if len(field.Expected) > 0 {
			line += " — expected: " + strings.Join(field.Expected, ", ")
		}
		fmt.Fprintln(os.Stderr, line)
	}
	return fmt.Errorf("fix the fields above and resubmit with --set key=value")
}

func mustFlag(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}

// shortDigest derives a stable idempotency suffix from the summary, so a
// retried submit is recognised as the same handoff rather than stacking a
// second revision.
func shortDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}
