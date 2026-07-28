package main

import (
	"context"
	"encoding/json"
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

var workflowSubmitCmd = &cobra.Command{
	Use:   "submit [issue-id]",
	Short: "Submit an artifact for the current node",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkflowSubmit,
}

func init() {
	workflowCmd.AddCommand(workflowCurrentCmd)
	workflowCmd.AddCommand(workflowArtifactsCmd)
	workflowCmd.AddCommand(workflowArtifactCmd)
	workflowArtifactCmd.AddCommand(workflowArtifactGetCmd)
	workflowCmd.AddCommand(workflowSubmitCmd)

	workflowCurrentCmd.Flags().String("output", "table", "Output format: table or json")
	workflowArtifactsCmd.Flags().String("output", "table", "Output format: table or json")
	workflowArtifactGetCmd.Flags().String("output", "text", "Output format: text or json")

	workflowSubmitCmd.Flags().String("artifact", "", "Artifact key declared by the node (required)")
	workflowSubmitCmd.Flags().String("file", "", "Read the document body from this file")
	workflowSubmitCmd.Flags().String("content", "", "Document body given inline")
	workflowSubmitCmd.Flags().String("url", "", "External address for a link artifact")
	workflowSubmitCmd.Flags().String("attachment-id", "", "Attachment id for an attachment artifact")
	workflowSubmitCmd.Flags().String("output", "table", "Output format: table or json")
	_ = workflowSubmitCmd.MarkFlagRequired("artifact")
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

func resolveWorkflowIssueID(args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return strings.TrimSpace(args[0]), nil
	}
	if issueID := daemonTaskIssueID(); issueID != "" {
		return issueID, nil
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

type workflowNodeSummary struct {
	ID       string `json:"id"`
	NodeKey  string `json:"node_key"`
	NodeKind string `json:"node_kind"`
	Name     string `json:"name_snapshot"`
	Status   string `json:"status"`
	Attempt  int32  `json:"attempt"`
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
func artifactBodyFromFlags(cmd *cobra.Command, artifactKey string) (map[string]any, error) {
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

	body := map[string]any{"artifact_key": artifactKey}
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

func runWorkflowSubmit(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	issueID, err := resolveWorkflowIssueID(args)
	if err != nil {
		return err
	}
	artifactKey, _ := cmd.Flags().GetString("artifact")
	body, err := artifactBodyFromFlags(cmd, strings.TrimSpace(artifactKey))
	if err != nil {
		return err
	}
	detail, nodeKey, err := resolveWorkflowContext(client, issueID)
	if err != nil {
		return err
	}
	if nodeKey == "" {
		return fmt.Errorf(
			"issue %s is not a workflow node issue; submit from the node's own issue", issueID,
		)
	}
	node, ok := currentNode(detail, nodeKey)
	if !ok {
		return fmt.Errorf("workflow node %q is not part of run %s", nodeKey, detail.Instance.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var response struct {
		Artifact workflowArtifactSummary `json:"artifact"`
		Replaced bool                    `json:"replaced"`
	}
	if err := client.PostJSON(
		ctx, "/api/workflow-node-instances/"+node.ID+"/artifacts", body, &response,
	); err != nil {
		return fmt.Errorf("submit artifact: %w", err)
	}
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, response)
	}
	verb := "submitted"
	if response.Replaced {
		verb = "replaced"
	}
	fmt.Fprintf(os.Stdout, "%s %s (%s)\n", verb, response.Artifact.Name, response.Artifact.ID)
	return nil
}
