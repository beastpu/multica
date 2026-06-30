package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/perforce"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// processPerforceReview upserts a review that references at least one issue in
// this workspace, (re)links it, and advances any issue whose linked reviews are
// all settled with a committing review. Returns whether an issue advanced.
func (h *Handler) processPerforceReview(
	ctx context.Context,
	conn db.PerforceConnection,
	prefix string,
	review perforce.Review,
) (bool, error) {
	// Resolve every identifier in the description to a real issue in this
	// workspace. Reviews that reference no issue are not stored — Swarm has
	// far more reviews than Multica issues.
	matched := map[string]db.Issue{}
	for _, ident := range extractIdentifiers(review.Description) {
		if issue, ok := h.lookupIssueByIdentifier(ctx, conn.WorkspaceID, prefix, ident); ok {
			matched[ident] = issue
		}
	}
	if len(matched) == 0 {
		return false, nil
	}

	stored, err := h.Queries.UpsertPerforceReview(ctx, db.UpsertPerforceReviewParams{
		WorkspaceID:     conn.WorkspaceID,
		ReviewID:        review.ID,
		Title:           review.Title,
		State:           review.State,
		HtmlUrl:         perforceReviewURL(conn.SwarmUrl, review.ID),
		Author:          pgTextOrNull(review.Author),
		ShelvedCl:       pgInt4OrNull(review.ShelvedCL),
		CommittedCl:     pgInt4OrNull(review.CommittedCL),
		ReviewCreatedAt: pgTimestamptz(review.CreatedAt),
		ReviewUpdatedAt: pgTimestamptz(review.UpdatedAt),
	})
	if err != nil {
		return false, err
	}

	// Product decision: a Perforce review that references an issue closes it on
	// submit — no closing keyword required (unlike GitHub's keyword gate). So
	// every linked review carries close intent; the commit gate below is what
	// actually advances the issue.
	for _, issue := range matched {
		if err := h.Queries.LinkIssueToPerforceReview(ctx, db.LinkIssueToPerforceReviewParams{
			IssueID:          issue.ID,
			PerforceReviewID: stored.ID,
			CloseIntent:      true,
			LinkedByType:     pgTextOrNull("system"),
		}); err != nil {
			return false, err
		}
	}

	// Only a committed review can resolve an issue.
	if !stored.CommittedCl.Valid {
		return false, nil
	}
	advancedAny := false
	wsIDStr := uuidToString(conn.WorkspaceID)
	for _, issue := range matched {
		if issue.Status == "done" || issue.Status == "cancelled" {
			continue
		}
		agg, err := h.Queries.GetIssuePerforceReviewCloseAggregate(ctx, issue.ID)
		if err != nil {
			return advancedAny, err
		}
		if agg.OpenCount == 0 && agg.CommittedWithCloseIntentCount > 0 {
			h.advanceIssueToDone(ctx, issue, wsIDStr, "perforce_review_committed")
			advancedAny = true
		}
	}
	return advancedAny, nil
}

// workspacePerforceEnabled reports whether the workspace has opted into
// Perforce. Unlike GitHub, Perforce is opt-in: it returns false unless
// `perforce_enabled` is explicitly true, so a workspace that never configures
// Perforce is never processed.
func (h *Handler) workspacePerforceEnabled(ctx context.Context, workspaceID pgtype.UUID) bool {
	ws, err := h.Queries.GetWorkspace(ctx, workspaceID)
	if err != nil || len(ws.Settings) == 0 {
		return false
	}
	var s struct {
		PerforceEnabled *bool `json:"perforce_enabled"`
	}
	if err := json.Unmarshal(ws.Settings, &s); err != nil {
		return false
	}
	return s.PerforceEnabled != nil && *s.PerforceEnabled
}

func perforceReviewURL(swarmURL string, reviewID int64) string {
	return fmt.Sprintf("%s/reviews/%d", strings.TrimRight(swarmURL, "/"), reviewID)
}

func pgTextOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func pgInt4OrNull(p *int64) pgtype.Int4 {
	if p == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*p), Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// ── HTTP: connection config ──────────────────────────────────────────────────

// PerforceConnectionResponse is the client shape for a workspace's Swarm
// connection. v1 is webhook-push only, so a connection is just the Swarm URL.
type PerforceConnectionResponse struct {
	WorkspaceID string `json:"workspace_id"`
	SwarmURL    string `json:"swarm_url"`
}

func perforceConnectionToResponse(c db.PerforceConnection) PerforceConnectionResponse {
	return PerforceConnectionResponse{
		WorkspaceID: uuidToString(c.WorkspaceID),
		SwarmURL:    c.SwarmUrl,
	}
}

// GetPerforceConnection (member) returns the workspace's Swarm connection, or a
// null connection when none is configured. configured reflects whether the
// webhook token is set (the integration cannot authenticate inbound pushes
// without it); can_manage gates the settings UI's edit controls.
func (h *Handler) GetPerforceConnection(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	member, _ := middleware.MemberFromContext(r.Context())
	canManage := roleAllowed(member.Role, "owner", "admin")

	conn, err := h.Queries.GetPerforceConnectionByWorkspace(r.Context(), wsUUID)
	body := map[string]any{
		"connection": nil,
		"configured": p4SwarmWebhookToken() != "",
		"can_manage": canManage,
	}
	switch {
	case err == nil:
		body["connection"] = perforceConnectionToResponse(conn)
	case errors.Is(err, pgx.ErrNoRows):
		// leave connection null
	default:
		writeError(w, http.StatusInternalServerError, "failed to load perforce connection")
		return
	}
	writeJSON(w, http.StatusOK, body)
}

type perforceConnectionRequest struct {
	SwarmURL string `json:"swarm_url"`
}

// SavePerforceConnection (admin) upserts the workspace's Swarm connection. v1
// stores only the Swarm URL the webhook routes on — no credentials.
func (h *Handler) SavePerforceConnection(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req perforceConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.SwarmURL = strings.TrimSpace(req.SwarmURL)
	if req.SwarmURL == "" {
		writeError(w, http.StatusBadRequest, "swarm_url is required")
		return
	}
	if !strings.HasPrefix(req.SwarmURL, "http://") && !strings.HasPrefix(req.SwarmURL, "https://") {
		writeError(w, http.StatusBadRequest, "swarm_url must start with http:// or https://")
		return
	}

	member, _ := middleware.MemberFromContext(r.Context())
	conn, err := h.Queries.UpsertPerforceConnection(r.Context(), db.UpsertPerforceConnectionParams{
		WorkspaceID:   wsUUID,
		SwarmUrl:      req.SwarmURL,
		ConnectedByID: member.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save perforce connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": perforceConnectionToResponse(conn)})
}

// ── HTTP: reviews for an issue ───────────────────────────────────────────────

// PerforceReviewResponse is the client shape for a review linked to an issue.
type PerforceReviewResponse struct {
	ReviewID    int64   `json:"review_id"`
	State       string  `json:"state"`
	Title       string  `json:"title"`
	HtmlURL     string  `json:"html_url"`
	Author      *string `json:"author,omitempty"`
	ShelvedCL   *int32  `json:"shelved_cl,omitempty"`
	CommittedCL *int32  `json:"committed_cl,omitempty"`
}

func perforceReviewToResponse(rev db.PerforceReview) PerforceReviewResponse {
	resp := PerforceReviewResponse{
		ReviewID: rev.ReviewID,
		State:    rev.State,
		Title:    rev.Title,
		HtmlURL:  rev.HtmlUrl,
	}
	if rev.Author.Valid {
		resp.Author = &rev.Author.String
	}
	if rev.ShelvedCl.Valid {
		resp.ShelvedCL = &rev.ShelvedCl.Int32
	}
	if rev.CommittedCl.Valid {
		resp.CommittedCL = &rev.CommittedCl.Int32
	}
	return resp
}

// ListPerforceReviewsForIssue (member) lists the Swarm reviews linked to an issue.
func (h *Handler) ListPerforceReviewsForIssue(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	rows, err := h.Queries.ListReviewsByIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list reviews")
		return
	}
	out := make([]PerforceReviewResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, perforceReviewToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": out})
}
