package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/integrations/perforce"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// p4SwarmWebhookToken is the shared bearer token the Swarm platform must send.
// Empty disables the endpoint (503) rather than trusting every caller — the
// public ingress is additionally IP-allowlisted, this is the application layer.
func p4SwarmWebhookToken() string {
	return strings.TrimSpace(os.Getenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN"))
}

// allowedSwarmStates mirrors the perforce_review.state CHECK constraint. A value
// outside this set is acknowledged and ignored (logged) rather than crashing the
// insert — Swarm enum drift downgrades, it does not 500.
var allowedSwarmStates = map[string]bool{
	"needsReview": true, "needsRevision": true, "approved": true, "rejected": true, "archived": true,
}

type p4SwarmWebhookPayload struct {
	EventType string `json:"event_type"`
	SentAt    string `json:"sent_at"`
	Swarm     struct {
		URL    string `json:"url"`
		Branch string `json:"branch"`
	} `json:"swarm"`
	Review struct {
		ID          int64   `json:"id"`
		State       string  `json:"state"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Author      string  `json:"author"`
		Changes     []int64 `json:"changes"`
		Commits     []int64 `json:"commits"`
		Created     int64   `json:"created"`
		Updated     int64   `json:"updated"`
	} `json:"review"`
}

// HandleP4SwarmWebhook (POST /api/webhooks/p4-swarm) ingests a full review
// snapshot pushed by the Swarm platform. master cannot call back to Swarm (it
// lives on the internal network), so the payload is self-contained and nothing
// here re-fetches from Swarm. Routing: swarm.url selects candidate connections
// (one Swarm can map to several workspaces), then the issue identifier in the
// description pins the workspace. Auth is a static bearer token.
func (h *Handler) HandleP4SwarmWebhook(w http.ResponseWriter, r *http.Request) {
	token := p4SwarmWebhookToken()
	if token == "" {
		writeError(w, http.StatusServiceUnavailable, "p4-swarm webhook not configured")
		return
	}
	if !validBearerToken(r.Header.Get("Authorization"), token) {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // review snapshots are small
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	var p p4SwarmWebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	swarmURL := strings.TrimSpace(p.Swarm.URL)
	if swarmURL == "" || p.Review.ID == 0 || p.Review.State == "" || p.Review.Updated == 0 {
		writeError(w, http.StatusBadRequest, "missing required fields (swarm.url, review.id, review.state, review.updated)")
		return
	}

	ctx := r.Context()
	if !allowedSwarmStates[p.Review.State] {
		// Unknown state would violate the perforce_review CHECK; ack + ignore.
		slog.Warn("p4-swarm webhook: unknown review state, ignoring", "state", p.Review.State, "review_id", p.Review.ID)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored"})
		return
	}

	review := perforce.Review{
		ID:          p.Review.ID,
		State:       p.Review.State,
		Title:       p.Review.Title,
		Author:      p.Review.Author,
		Description: p.Review.Description,
		ShelvedCL:   maxChangelist(p.Review.Changes),
		CommittedCL: maxChangelist(p.Review.Commits),
		CreatedAt:   time.Unix(p.Review.Created, 0).UTC(),
		UpdatedAt:   time.Unix(p.Review.Updated, 0).UTC(),
	}

	candidates, err := h.Queries.ListPerforceConnectionsBySwarmURL(ctx, swarmURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup connections failed")
		return
	}

	// swarm.url is not a unique routing key — resolve which enabled candidate
	// workspace actually owns an issue referenced in the description.
	idents := extractIdentifiers(review.Description)
	var matched []db.PerforceConnection
	for _, c := range candidates {
		if !h.workspacePerforceEnabled(ctx, c.WorkspaceID) {
			continue
		}
		prefix := h.getIssuePrefix(ctx, c.WorkspaceID)
		for _, ident := range idents {
			if _, ok := h.lookupIssueByIdentifier(ctx, c.WorkspaceID, prefix, ident); ok {
				matched = append(matched, c)
				break
			}
		}
	}

	switch len(matched) {
	case 0:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored"})
		return
	case 1:
		// handled below
	default:
		slog.Warn("p4-swarm webhook: review matches issues in multiple workspaces, skipping",
			"review_id", review.ID, "swarm_url", swarmURL, "candidates", len(matched))
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ambiguous"})
		return
	}

	conn := matched[0]

	// Idempotency + ordering via the stored watermark: a retry carries the same
	// review.updated, a delayed older event a smaller one. Skip if we already
	// stored a review at least as new, so a stale event cannot clobber
	// committed_cl/state.
	existing, err := h.Queries.GetPerforceReviewByWorkspaceReviewID(ctx, db.GetPerforceReviewByWorkspaceReviewIDParams{
		WorkspaceID: conn.WorkspaceID,
		ReviewID:    review.ID,
	})
	switch {
	case err == nil:
		if existing.ReviewUpdatedAt.Valid && !existing.ReviewUpdatedAt.Time.Before(review.UpdatedAt) {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate"})
			return
		}
	case errors.Is(err, pgx.ErrNoRows):
		// first time for this review — proceed
	default:
		writeError(w, http.StatusInternalServerError, "lookup review failed")
		return
	}

	if _, err := h.processPerforceReview(ctx, conn, h.getIssuePrefix(ctx, conn.WorkspaceID), review); err != nil {
		writeError(w, http.StatusInternalServerError, "process review failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "processed"})
}

// validBearerToken constant-time-compares the `Authorization: Bearer <token>`
// header against the configured token.
func validBearerToken(header, token string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// maxChangelist returns the largest changelist number (the latest, since CL
// numbers are monotonic) or nil for an empty list. Swarm sends arrays; Multica
// stores a single shelved/committed CL.
func maxChangelist(xs []int64) *int64 {
	if len(xs) == 0 {
		return nil
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return &m
}
