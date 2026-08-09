package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// maxWorkflowArtifactContentBytes mirrors the database CHECK. Rejecting here
// turns an oversized document into a 400 that names the limit, instead of a
// constraint violation surfacing as a 500.
const maxWorkflowArtifactContentBytes = 1 << 20

type workflowArtifactResponse struct {
	ID                     string  `json:"id"`
	WorkflowInstanceID     string  `json:"workflow_instance_id"`
	WorkflowNodeInstanceID string  `json:"workflow_node_instance_id"`
	ArtifactKey            string  `json:"artifact_key"`
	Attempt                int32   `json:"attempt"`
	Kind                   string  `json:"kind"`
	Name                   string  `json:"name"`
	Description            string  `json:"description"`
	Content                string  `json:"content,omitempty"`
	AttachmentID           *string `json:"attachment_id,omitempty"`
	URL                    string  `json:"url,omitempty"`
	ReviewStatus           string  `json:"review_status"`
	ReviewComment          string  `json:"review_comment,omitempty"`
	ReviewedBy             *string `json:"reviewed_by,omitempty"`
	ReviewedAt             *string `json:"reviewed_at,omitempty"`
	SubmittedByType        string  `json:"submitted_by_type"`
	SubmittedByID          *string `json:"submitted_by_id,omitempty"`
	CreatedAt              string  `json:"created_at"`
	UpdatedAt              string  `json:"updated_at"`
}

func workflowArtifactToResponse(row db.WorkflowArtifact) workflowArtifactResponse {
	return workflowArtifactResponse{
		ID:                     uuidToString(row.ID),
		WorkflowInstanceID:     uuidToString(row.WorkflowInstanceID),
		WorkflowNodeInstanceID: uuidToString(row.WorkflowNodeInstanceID),
		ArtifactKey:            row.ArtifactKey,
		Attempt:                row.Attempt,
		Kind:                   row.Kind,
		Name:                   row.Name,
		Description:            row.Description,
		Content:                row.Content,
		AttachmentID:           uuidToPtr(row.AttachmentID),
		URL:                    row.Url,
		ReviewStatus:           row.ReviewStatus,
		ReviewComment:          row.ReviewComment,
		ReviewedBy:             uuidToPtr(row.ReviewedBy),
		ReviewedAt:             timestampToPtr(row.ReviewedAt),
		SubmittedByType:        row.SubmittedByType,
		SubmittedByID:          uuidToPtr(row.SubmittedByID),
		CreatedAt:              timestampToString(row.CreatedAt),
		UpdatedAt:              timestampToString(row.UpdatedAt),
	}
}

func workflowArtifactsResponse(rows []db.WorkflowArtifact) []workflowArtifactResponse {
	items := make([]workflowArtifactResponse, len(rows))
	for index, row := range rows {
		items[index] = workflowArtifactToResponse(row)
	}
	return items
}

// reviewWorkflowArtifactsForVerdict applies one Critic decision to the exact
// live artifact revisions reviewed with the node submission. Callers run this
// inside the same transaction that records the node verdict, so a partial
// artifact review can never survive a failed verdict write.
//
// A passing verdict approves every delivered artifact and requires every
// required slot to be present. A failing verdict rejects the delivered set so
// the Worker knows each revision belongs to the rejected delivery. A blocked
// verdict records the snapshot without judging the artifacts: the blocker may
// be external rather than a defect in the delivery.
func reviewWorkflowArtifactsForVerdict(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	result string,
	comment string,
	reviewerID pgtype.UUID,
) ([]string, error) {
	artifacts, err := q.ListWorkflowNodeArtifacts(ctx, db.ListWorkflowNodeArtifactsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return nil, err
	}
	delivered := make(map[string]db.WorkflowArtifact, len(artifacts))
	artifactIDs := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		delivered[artifact.ArtifactKey] = artifact
		artifactIDs = append(artifactIDs, uuidToString(artifact.ID))
	}
	if result == "pass" {
		for _, requirement := range workflowdomain.RequiredArtifacts(nodeDefinition) {
			if _, exists := delivered[requirement.Key]; !exists {
				return nil, fmt.Errorf("required artifact %q has not been submitted", requirement.Key)
			}
		}
	}

	targetStatus := ""
	switch result {
	case "pass":
		targetStatus = "approved"
	case "fail":
		targetStatus = "rejected"
	}
	if targetStatus == "" {
		return artifactIDs, nil
	}
	for _, artifact := range artifacts {
		if artifact.ReviewStatus == targetStatus && artifact.ReviewComment == comment &&
			artifact.ReviewedBy == reviewerID {
			continue
		}
		if _, err := q.ReviewWorkflowArtifact(ctx, db.ReviewWorkflowArtifactParams{
			ID: artifact.ID, WorkspaceID: workspaceID,
			ReviewStatus: targetStatus, ReviewComment: comment,
			ReviewedBy: reviewerID,
		}); err != nil {
			return nil, err
		}
	}
	return artifactIDs, nil
}

// workflowArtifactApprovalReasons is the final post-verdict gate. Submission is
// enough to enter review, but never enough to leave it: every required artifact
// revision must carry the same Critic approval as the passing node verdict.
func workflowArtifactApprovalReasons(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
) ([]workflowdomain.WaitingReason, error) {
	required := workflowdomain.RequiredArtifacts(nodeDefinition)
	if len(required) == 0 {
		return nil, nil
	}
	artifacts, err := q.ListWorkflowNodeArtifacts(ctx, db.ListWorkflowNodeArtifactsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return nil, err
	}
	delivered := make(map[string]db.WorkflowArtifact, len(artifacts))
	for _, artifact := range artifacts {
		delivered[artifact.ArtifactKey] = artifact
	}
	reasons := make([]workflowdomain.WaitingReason, 0)
	for _, requirement := range required {
		artifact, exists := delivered[requirement.Key]
		if !exists {
			reasons = append(reasons, workflowdomain.WaitingReason{
				Code: "required_artifact_missing", Field: requirement.Key,
				Message: "Required artifact has not been submitted",
			})
			continue
		}
		if artifact.ReviewStatus != "approved" {
			reasons = append(reasons, workflowdomain.WaitingReason{
				Code: "required_artifact_review_pending", Field: requirement.Key,
				Message: "Required artifact has not been approved by the reviewer",
			})
		}
	}
	return reasons, nil
}

// publishWorkflowArtifactsReviewed emits invalidation signals only after the
// verdict transaction commits. Consumers therefore never observe an artifact
// decision without its matching node verdict (or the reverse).
func (h *Handler) publishWorkflowArtifactsReviewed(
	workspaceID pgtype.UUID,
	instanceID pgtype.UUID,
	nodeID pgtype.UUID,
	actorType string,
	actorID string,
	artifactIDs []string,
	reviewStatus string,
) {
	if reviewStatus != "approved" && reviewStatus != "rejected" {
		return
	}
	for _, artifactID := range artifactIDs {
		h.publishWorkflowRealtime(
			protocol.EventWorkflowArtifactReviewed,
			uuidToString(workspaceID), actorType, actorID,
			map[string]any{
				"workflow_instance_id":      uuidToString(instanceID),
				"workflow_node_instance_id": uuidToString(nodeID),
				"artifact_id":               artifactID,
				"review_status":             reviewStatus,
			},
		)
	}
}

type submitWorkflowArtifactRequest struct {
	ArtifactKey  string  `json:"artifact_key"`
	Content      string  `json:"content,omitempty"`
	AttachmentID *string `json:"attachment_id,omitempty"`
	URL          string  `json:"url,omitempty"`
	// IssueID is the node issue the submitter is working in. Optional: when
	// present the submission leaves a trace on that issue, so delivering does
	// not also require posting a comment to make the work visible to people.
	// Older CLIs omit it and simply get no trace.
	IssueID string `json:"issue_id,omitempty"`
}

// findArtifactRequirement locates the declaration an incoming artifact claims
// to satisfy. Submissions are only accepted against a declared requirement:
// the node definition is what tells the downstream index what an artifact is
// called and what it should cover, and an undeclared one would have neither.
func findArtifactRequirement(
	nodeDefinition workflowdomain.NodeDefinition,
	key string,
) (workflowdomain.ArtifactRequirement, bool) {
	for _, requirement := range nodeDefinition.Artifacts {
		if requirement.Key == key {
			return requirement, true
		}
	}
	return workflowdomain.ArtifactRequirement{}, false
}

// ListWorkflowNodeArtifacts returns the live artifacts of one node instance.
func (h *Handler) ListWorkflowNodeArtifacts(w http.ResponseWriter, r *http.Request) {
	node, _, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListWorkflowNodeArtifacts(r.Context(), db.ListWorkflowNodeArtifactsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow artifacts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"artifacts": workflowArtifactsResponse(rows),
	})
}

// ListWorkflowInstanceArtifacts returns every live artifact in one run. This is
// the "queryable but not auto-injected" half of artifact sharing: any node may
// look the whole run up, while prompts only carry the direct predecessors'.
func (h *Handler) ListWorkflowInstanceArtifacts(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListWorkflowInstanceArtifacts(
		r.Context(),
		db.ListWorkflowInstanceArtifactsParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow artifacts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"artifacts": workflowArtifactsResponse(rows),
	})
}

// SubmitWorkflowArtifact records an artifact against a node instance,
// replacing whatever that node last delivered under the same key.
//
// The replacement retires the previous row rather than updating it, so an
// agent that overwrites a sound document during rework cannot destroy it.
func (h *Handler) SubmitWorkflowArtifact(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	node, instance, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	var req submitWorkflowArtifactRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ArtifactKey = strings.TrimSpace(req.ArtifactKey)
	if req.ArtifactKey == "" {
		writeError(w, http.StatusBadRequest, "artifact_key is required")
		return
	}
	if instance.Status != "running" || !workflowNodeIsOpen(node) {
		writeError(w, http.StatusConflict, "workflow node does not accept artifacts")
		return
	}
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow node snapshot")
		return
	}
	requirement, declared := findArtifactRequirement(nodeDefinition, req.ArtifactKey)
	if !declared {
		writeError(w, http.StatusBadRequest, "workflow node does not declare this artifact")
		return
	}
	kind := workflowdomain.ArtifactKind(requirement)

	// Exactly one carrier must match the declared kind. The database enforces
	// this too, but a mismatch here is a caller error worth naming.
	content, url := "", strings.TrimSpace(req.URL)
	var attachmentID pgtype.UUID
	switch kind {
	case "document":
		content = req.Content
		if strings.TrimSpace(content) == "" {
			writeError(w, http.StatusBadRequest, "content is required for a document artifact")
			return
		}
		if len(content) > maxWorkflowArtifactContentBytes {
			writeError(w, http.StatusBadRequest, "artifact content exceeds 1 MB; submit it as an attachment")
			return
		}
		if req.AttachmentID != nil || url != "" {
			writeError(w, http.StatusBadRequest, "a document artifact carries content only")
			return
		}
	case "attachment":
		if req.AttachmentID == nil {
			writeError(w, http.StatusBadRequest, "attachment_id is required for an attachment artifact")
			return
		}
		parsed, valid := parseUUIDOrBadRequest(w, *req.AttachmentID, "attachment_id")
		if !valid {
			return
		}
		if _, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{
			ID: parsed, WorkspaceID: node.WorkspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusBadRequest, "attachment not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load attachment")
			return
		}
		attachmentID = parsed
		if strings.TrimSpace(req.Content) != "" || url != "" {
			writeError(w, http.StatusBadRequest, "an attachment artifact carries attachment_id only")
			return
		}
	case "link":
		if url == "" {
			writeError(w, http.StatusBadRequest, "url is required for a link artifact")
			return
		}
		// A link artifact is submitted by an agent and later rendered as a
		// clickable address, so the scheme is an injection boundary: anything
		// but http(s) — javascript:, data:, file: — turns a stored artifact
		// into code that runs when someone opens it.
		if !isBrowsableArtifactURL(url) {
			writeError(w, http.StatusBadRequest, "url must be an http or https address")
			return
		}
		if strings.TrimSpace(req.Content) != "" || req.AttachmentID != nil {
			writeError(w, http.StatusBadRequest, "a link artifact carries url only")
			return
		}
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorIDText := h.resolveActor(r, userID, uuidToString(node.WorkspaceID))
	actorID, ok := parseUUIDOrBadRequest(w, actorIDText, "actor_id")
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.LockWorkflowInstance(r.Context(), db.LockWorkflowInstanceParams{
		ID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	currentNode, err := qtx.GetWorkflowNodeInstanceInWorkspace(
		r.Context(),
		db.GetWorkflowNodeInstanceInWorkspaceParams{
			ID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil || locked.Status != "running" || !workflowNodeIsOpen(currentNode) {
		writeError(w, http.StatusConflict, "workflow node does not accept artifacts")
		return
	}
	if currentNode.Status == "in_review" {
		writeError(w, http.StatusConflict, "workflow artifacts are frozen while the node is in review")
		return
	}
	node = currentNode

	replaced, err := qtx.SupersedeWorkflowArtifact(r.Context(), db.SupersedeWorkflowArtifactParams{
		WorkspaceID: node.WorkspaceID, WorkflowNodeInstanceID: node.ID,
		ArtifactKey: req.ArtifactKey,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to supersede workflow artifact")
		return
	}
	artifact, err := qtx.CreateWorkflowArtifact(r.Context(), db.CreateWorkflowArtifactParams{
		WorkspaceID: node.WorkspaceID, WorkflowInstanceID: instance.ID,
		WorkflowNodeInstanceID: node.ID, ArtifactKey: req.ArtifactKey,
		Attempt: node.Attempt, Kind: kind, Name: requirement.Name,
		Description: requirement.Description,
		Content:     content, AttachmentID: attachmentID, Url: url,
		SubmittedByType: actorType, SubmittedByID: actorID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record workflow artifact")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow artifact")
		return
	}

	h.noteArtifactOnIssue(r, node, artifact, req.IssueID, replaced > 0)
	h.publishWorkflowRealtime(
		protocol.EventWorkflowArtifactSubmitted,
		uuidToString(node.WorkspaceID), actorType, actorIDText,
		map[string]any{
			"workflow_instance_id":      uuidToString(instance.ID),
			"workflow_node_instance_id": uuidToString(node.ID),
			"artifact_id":               uuidToString(artifact.ID),
			"artifact_key":              artifact.ArtifactKey,
			"replaced":                  replaced > 0,
		},
	)
	h.WorkflowReconciler.Notify()
	writeJSON(w, http.StatusOK, map[string]any{
		"artifact": workflowArtifactToResponse(artifact),
		"replaced": replaced > 0,
	})
}

// maxArtifactCommentPreview keeps the trace readable in a comment thread. The
// artifact itself is the record; this is a pointer to it.
const maxArtifactCommentPreview = 400

// noteArtifactOnIssue records a delivery on the node's issue.
//
// Submitting an artifact and posting it for people to see used to be two
// actions: agents attached the file to a comment so humans could read it, then
// submitted the same content again so the node could advance. Same file, twice,
// through two paths — and forgetting the second one silently blocked the run.
// One action now does both.
//
// Best-effort on purpose. The artifact is already committed and it is what
// gates the node; failing the request because a courtesy comment could not be
// written would trade the delivery for its announcement.
// resolveWorkflowArtifactIssue picks the issue an artifact submission should be
// traced onto: the one the caller named, or else the node's own — but only when
// the node has exactly one. A node running several issue-backed tasks has no
// single owner for a node-level artifact, and picking one of them would file
// the delivery under work it did not come from.
func (h *Handler) resolveWorkflowArtifactIssue(
	ctx context.Context,
	node db.WorkflowNodeInstance,
	requested string,
) (pgtype.UUID, bool) {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		parsed, err := util.ParseUUID(trimmed)
		if err != nil {
			return pgtype.UUID{}, false
		}
		return parsed, true
	}
	tasks, err := h.Queries.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		return pgtype.UUID{}, false
	}
	var sole pgtype.UUID
	for _, task := range tasks {
		if !task.IssueID.Valid {
			continue
		}
		if sole.Valid {
			return pgtype.UUID{}, false
		}
		sole = task.IssueID
	}
	return sole, sole.Valid
}

func (h *Handler) noteArtifactOnIssue(
	r *http.Request,
	node db.WorkflowNodeInstance,
	artifact db.WorkflowArtifact,
	issueID string,
	replaced bool,
) {
	// A submitter that names no issue still delivered to a node, and the node
	// usually owns one. Requiring the caller to supply it meant the trace was
	// written exactly when it was least needed — by a caller already holding
	// the issue — and skipped when an agent submitted through the node alone.
	parsed, ok := h.resolveWorkflowArtifactIssue(r.Context(), node, issueID)
	if !ok {
		return
	}
	issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID: parsed, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		return
	}
	// An attachment artifact lives in object storage addressed by the artifact
	// row. Announcing it on the issue without binding it there produced the
	// exact failure the delivery rules forbid: a comment that says a file was
	// delivered, next to no file.
	if artifact.Kind == "attachment" && artifact.AttachmentID.Valid {
		if err := h.Queries.LinkAttachmentsToIssue(r.Context(), db.LinkAttachmentsToIssueParams{
			IssueID: issue.ID, WorkspaceID: node.WorkspaceID,
			Column3: []pgtype.UUID{artifact.AttachmentID},
		}); err != nil {
			slog.Warn("workflow artifact: attachment link failed",
				"error", err,
				"artifact_id", uuidToString(artifact.ID),
				"issue_id", uuidToString(issue.ID),
			)
		}
	}

	var body strings.Builder
	verb := "Submitted"
	if replaced {
		verb = "Replaced"
	}
	fmt.Fprintf(&body, "%s artifact **%s** (`%s`).", verb, artifact.Name, artifact.ArtifactKey)
	switch artifact.Kind {
	case "link":
		fmt.Fprintf(&body, "\n\n%s", artifact.Url)
	case "attachment":
		body.WriteString("\n\nDelivered as a file attachment on this issue.")
	default:
		preview := []rune(strings.TrimSpace(artifact.Content))
		if len(preview) > maxArtifactCommentPreview {
			fmt.Fprintf(&body, "\n\n%s…", string(preview[:maxArtifactCommentPreview]))
		} else if len(preview) > 0 {
			fmt.Fprintf(&body, "\n\n%s", string(preview))
		}
	}

	if _, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: node.WorkspaceID,
		// author_type='system' with the zero UUID, matching the other
		// machine-written comments; the frontend branches on author_type.
		AuthorType: "system",
		AuthorID:   pgtype.UUID{Valid: true},
		Content:    body.String(),
		Type:       "system",
		ParentID:   pgtype.UUID{Valid: false},
	}); err != nil {
		slog.Warn("workflow artifact: issue trace failed",
			"error", err,
			"artifact_id", uuidToString(artifact.ID),
			"issue_id", issueID,
		)
	}
}

type reviewWorkflowArtifactRequest struct {
	Status  string `json:"status"`
	Comment string `json:"comment,omitempty"`
}

// ReviewWorkflowArtifact records an approval or rejection against the live
// artifact. A rejection blocks node completion the same way a missing artifact
// does; an approval only holds until the artifact is replaced.
func (h *Handler) ReviewWorkflowArtifact(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	node, _, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	artifactID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "artifactId"), "artifact_id")
	if !ok {
		return
	}
	var req reviewWorkflowArtifactRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch req.Status {
	case "approved", "rejected":
	default:
		writeError(w, http.StatusBadRequest, "status must be approved or rejected")
		return
	}
	existing, err := h.Queries.GetWorkflowArtifact(r.Context(), db.GetWorkflowArtifactParams{
		ID: artifactID, WorkspaceID: node.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) || existing.WorkflowNodeInstanceID != node.ID {
		writeError(w, http.StatusNotFound, "workflow artifact not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow artifact")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	reviewer, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	artifact, err := h.Queries.ReviewWorkflowArtifact(r.Context(), db.ReviewWorkflowArtifactParams{
		ID: artifactID, WorkspaceID: node.WorkspaceID,
		ReviewStatus: req.Status, ReviewComment: strings.TrimSpace(req.Comment),
		ReviewedBy: reviewer,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The row was superseded between the read and the write. Reviewing
		// retired content would record a decision about something no longer in
		// play, so the caller has to re-read and decide again.
		writeError(w, http.StatusConflict, "workflow artifact was replaced; refresh and try again")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to review workflow artifact")
		return
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowArtifactReviewed,
		uuidToString(node.WorkspaceID), "member", userID,
		map[string]any{
			"workflow_instance_id":      uuidToString(artifact.WorkflowInstanceID),
			"workflow_node_instance_id": uuidToString(node.ID),
			"artifact_id":               uuidToString(artifact.ID),
			"artifact_key":              artifact.ArtifactKey,
			"review_status":             artifact.ReviewStatus,
		},
	)
	h.WorkflowReconciler.Notify()
	writeJSON(w, http.StatusOK, map[string]any{
		"artifact": workflowArtifactToResponse(artifact),
	})
}

// Summary is the conclusion its author wrote; WorkerOutput is the platform's
// fallback when nobody wrote one, kept in its own field so the two are never
// confused. Issues points at the full record behind the summary.
type workflowUpstreamNode struct {
	NodeKey      string                    `json:"node_key"`
	Name         string                    `json:"name"`
	Status       string                    `json:"status"`
	Summary      string                    `json:"summary"`
	WorkerOutput string                    `json:"worker_output"`
	Issues       []string                  `json:"issues"`
	Artifacts    []workflowArtifactSummary `json:"artifacts"`
}

// workflowArtifactSummary is the index form of an artifact: enough to decide
// whether to read it, without the body. Carrying bodies here would make every
// downstream node pay for prose it may not need.
type workflowArtifactSummary struct {
	ID           string `json:"id"`
	ArtifactKey  string `json:"artifact_key"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	ReviewStatus string `json:"review_status"`
}

func workflowArtifactSummaries(rows []db.WorkflowArtifact) []workflowArtifactSummary {
	items := make([]workflowArtifactSummary, len(rows))
	for index, row := range rows {
		items[index] = workflowArtifactSummary{
			ID: uuidToString(row.ID), ArtifactKey: row.ArtifactKey,
			Kind: row.Kind, Name: row.Name, Description: row.Description,
			ReviewStatus: row.ReviewStatus,
		}
	}
	return items
}

// GetWorkflowNodeUpstream returns the handoff summary and artifact index of
// each direct predecessor of a node.
//
// Direct predecessors only, because that is what the graph says this node
// depends on. Walking further back would pull in parallel branches this node
// never waited for, and the whole point of the summary is to be small enough
// to read without deciding to.
func (h *Handler) GetWorkflowNodeUpstream(w http.ResponseWriter, r *http.Request) {
	node, instance, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	version, err := h.Queries.GetWorkflowVersionInWorkspace(
		r.Context(),
		db.GetWorkflowVersionInWorkspaceParams{
			ID: instance.WorkflowVersionID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow definition")
		return
	}
	definition, err := workflowdomain.ParseDefinition(version.Definition)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow definition")
		return
	}
	plan, err := workflowdomain.BuildGraphPlan(definition)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow graph")
		return
	}
	predecessors := make(map[string]struct{}, len(plan.Incoming[node.NodeKey]))
	for _, edge := range plan.Incoming[node.NodeKey] {
		predecessors[edge.From] = struct{}{}
	}

	nodes, err := h.Queries.ListWorkflowNodeInstances(r.Context(), db.ListWorkflowNodeInstancesParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow nodes")
		return
	}
	// Several attempts of the same node can exist after a rework. The live one
	// is the highest attempt; an earlier attempt's conclusion described work
	// that has since been redone.
	live := make(map[string]db.WorkflowNodeInstance, len(predecessors))
	for _, candidate := range nodes {
		if _, wanted := predecessors[candidate.NodeKey]; !wanted {
			continue
		}
		if existing, seen := live[candidate.NodeKey]; !seen || candidate.Attempt > existing.Attempt {
			live[candidate.NodeKey] = candidate
		}
	}

	upstream := make([]workflowUpstreamNode, 0, len(live))
	for _, definitionNode := range plan.Ordered {
		candidate, exists := live[definitionNode.Key]
		if !exists {
			continue
		}
		entry := workflowUpstreamNode{
			NodeKey: candidate.NodeKey, Name: candidate.NameSnapshot,
			Status: candidate.Status, Artifacts: []workflowArtifactSummary{},
			Issues: []string{},
		}
		if candidate.LatestSubmissionID.Valid {
			submission, err := h.Queries.GetWorkflowSubmissionInWorkspace(
				r.Context(),
				db.GetWorkflowSubmissionInWorkspaceParams{
					ID: candidate.LatestSubmissionID, WorkspaceID: instance.WorkspaceID,
				},
			)
			// A synthesised submission carries a canned summary; showing it as
			// the conclusion would attribute platform text to the executor.
			// The worker-output fallback below is the honest channel for it.
			if err == nil && submission.SubmittedByType != "system" {
				entry.Summary = submission.Summary
			}
		}
		if strings.TrimSpace(entry.Summary) == "" {
			entry.WorkerOutput = clipRunes(
				h.workflowDirectWorkerOutput(r.Context(), instance.WorkspaceID, candidate),
				upstreamWorkerOutputRunes,
			)
		}
		if issues := h.workflowNodeIssueIdentifiers(
			r.Context(), instance.WorkspaceID, candidate,
		); len(issues) > 0 {
			entry.Issues = issues
		}
		artifacts, err := h.Queries.ListWorkflowNodeArtifacts(
			r.Context(),
			db.ListWorkflowNodeArtifactsParams{
				WorkflowNodeInstanceID: candidate.ID, WorkspaceID: instance.WorkspaceID,
			},
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load workflow artifacts")
			return
		}
		entry.Artifacts = workflowArtifactSummaries(artifacts)
		upstream = append(upstream, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"upstream": upstream})
}

// isBrowsableArtifactURL accepts only the schemes safe to hand a browser.
func isBrowsableArtifactURL(raw string) bool {
	parsed, err := neturl.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}
