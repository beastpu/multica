package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/perforce"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/scheduler"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// JobNamePollPerforceReviews is the canonical scheduler job name. Stable
	// across releases — it is the audit/index key.
	JobNamePollPerforceReviews = "poll_perforce_reviews"

	// perforceDiscoveryPageSize is the Swarm page size for the id-descending
	// discovery scan.
	perforceDiscoveryPageSize = 50
	// perforceMaxDiscoveryPerTick bounds how many reviews a single tick scans,
	// which also bootstraps a fresh connection (cursor 0) to the most recent
	// window instead of backfilling all history.
	perforceMaxDiscoveryPerTick = 300
	// perforceProgressBatch bounds how many in-flight reviews are re-fetched
	// per tick to catch approval/submit progress.
	perforceProgressBatch = 100
)

// perforceSyncResult reports what one workspace poll changed, for the audit row.
type perforceSyncResult struct {
	Discovered     int
	ProgressPolled int
	Advanced       int
}

// PerforcePollJob returns the scheduler spec that polls Swarm reviews for every
// connected workspace. Each workspace is a separate scope so the lease is held
// per-workspace: a slow or failing Swarm for one workspace cannot block others,
// and two replicas can split the workspaces. The handler owns its own cursor,
// so latest-only catch-up is sufficient.
func (h *Handler) PerforcePollJob() scheduler.JobSpec {
	return scheduler.JobSpec{
		Name:              JobNamePollPerforceReviews,
		Cadence:           2 * time.Minute,
		CatchUpMode:       scheduler.CatchUpLatestOnly,
		CatchUpWindow:     1 * time.Hour,
		RunTimeout:        2 * time.Minute,
		StaleTimeout:      5 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{30 * time.Second, 2 * time.Minute},
		Scopes:            h.perforcePollScopes,
		Handler:           h.perforcePollHandler,
	}
}

// perforcePollScopes lists one scope per connected workspace. Returns nothing
// when the integration is disabled (no master key) so the job idles cheaply.
func (h *Handler) perforcePollScopes(ctx context.Context, _ time.Time) ([]scheduler.Scope, error) {
	if h.PerforceBox == nil {
		return nil, nil
	}
	conns, err := h.Queries.ListPerforceConnectionsForPolling(ctx)
	if err != nil {
		return nil, err
	}
	scopes := make([]scheduler.Scope, 0, len(conns))
	for _, c := range conns {
		scopes = append(scopes, scheduler.Scope{Kind: "workspace", ID: uuidToString(c.WorkspaceID)})
	}
	return scopes, nil
}

func (h *Handler) perforcePollHandler(ctx context.Context, in scheduler.HandlerInput) (scheduler.HandlerResult, error) {
	wsID, err := parseStrictUUID(in.Scope.ID)
	if err != nil {
		return scheduler.HandlerResult{}, fmt.Errorf("perforce: bad scope id %q: %w", in.Scope.ID, err)
	}
	conn, err := h.Queries.GetPerforceConnectionByWorkspace(ctx, wsID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Connection removed between scope listing and run — nothing to do.
			return scheduler.HandlerResult{}, nil
		}
		return scheduler.HandlerResult{}, err
	}
	res, err := h.SyncPerforceWorkspace(ctx, conn)
	if err != nil {
		return scheduler.HandlerResult{}, err
	}
	return scheduler.HandlerResult{
		RowsAffected: int64(res.Discovered + res.Advanced),
		Result: map[string]any{
			"discovered":      res.Discovered,
			"progress_polled": res.ProgressPolled,
			"advanced":        res.Advanced,
		},
	}, nil
}

// SyncPerforceWorkspace runs one poll for a single connection: discover new
// issue-linked reviews (id-descending until the cursor), then re-fetch in-flight
// reviews to catch approval/submit. Idempotent under retry — every write is an
// upsert keyed by (workspace_id, review_id) or the link primary key.
func (h *Handler) SyncPerforceWorkspace(ctx context.Context, conn db.PerforceConnection) (perforceSyncResult, error) {
	var res perforceSyncResult
	if h.PerforceBox == nil {
		return res, nil
	}
	if !h.workspacePerforceEnabled(ctx, conn.WorkspaceID) {
		return res, nil
	}
	client, err := h.swarmClientFor(conn)
	if err != nil {
		return res, err
	}
	prefix := h.getIssuePrefix(ctx, conn.WorkspaceID)

	if err := h.discoverPerforceReviews(ctx, conn, prefix, client, &res); err != nil {
		return res, err
	}
	if err := h.advanceInFlightPerforceReviews(ctx, conn, prefix, client, &res); err != nil {
		return res, err
	}
	return res, nil
}

// discoverPerforceReviews pages newest-first until it reaches the stored cursor
// or the per-tick cap, processing reviews that reference an issue, then advances
// the cursor to the highest id seen.
func (h *Handler) discoverPerforceReviews(
	ctx context.Context,
	conn db.PerforceConnection,
	prefix string,
	client perforce.Client,
	res *perforceSyncResult,
) error {
	cursor := conn.LastSeenReviewID
	maxID := cursor
	var after int64
	scanned := 0

	for scanned < perforceMaxDiscoveryPerTick {
		page, next, err := client.ListReviewsPage(ctx, after, perforceDiscoveryPageSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		reachedWatermark := false
		for _, review := range page {
			if review.ID <= cursor {
				reachedWatermark = true
				break
			}
			if review.ID > maxID {
				maxID = review.ID
			}
			advanced, err := h.processPerforceReview(ctx, conn, prefix, review)
			if err != nil {
				return err
			}
			if advanced {
				res.Advanced++
			}
			res.Discovered++
			scanned++
		}
		if reachedWatermark || next <= 0 || scanned >= perforceMaxDiscoveryPerTick {
			break
		}
		after = next
	}

	if maxID > cursor {
		if err := h.Queries.UpdatePerforceConnectionPollCursor(ctx, db.UpdatePerforceConnectionPollCursorParams{
			ID:               conn.ID,
			LastSeenReviewID: maxID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// advanceInFlightPerforceReviews re-fetches reviews that are stored but not yet
// terminal, so a commit/approval on an older review (deep in the id list) is
// still caught without re-scanning history.
func (h *Handler) advanceInFlightPerforceReviews(
	ctx context.Context,
	conn db.PerforceConnection,
	prefix string,
	client perforce.Client,
	res *perforceSyncResult,
) error {
	inflight, err := h.Queries.ListInFlightPerforceReviews(ctx, db.ListInFlightPerforceReviewsParams{
		WorkspaceID: conn.WorkspaceID,
		Limit:       perforceProgressBatch,
	})
	if err != nil {
		return err
	}
	for _, row := range inflight {
		review, found, err := client.GetReview(ctx, row.ReviewID)
		if err != nil {
			// One review failing must not abandon the rest of the batch.
			slog.Warn("perforce: get review failed", "review_id", row.ReviewID, "err", err)
			continue
		}
		if !found {
			continue
		}
		advanced, err := h.processPerforceReview(ctx, conn, prefix, review)
		if err != nil {
			return err
		}
		res.ProgressPolled++
		if advanced {
			res.Advanced++
		}
	}
	return nil
}

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

// swarmClientFor decrypts the connection's ticket and builds a Swarm client.
func (h *Handler) swarmClientFor(conn db.PerforceConnection) (perforce.Client, error) {
	if h.PerforceBox == nil {
		return nil, errors.New("perforce: secret key not configured")
	}
	if len(conn.SwarmTicketEncrypted) == 0 {
		return nil, errors.New("perforce: connection has no stored credential")
	}
	ticket, err := h.PerforceBox.Open(conn.SwarmTicketEncrypted)
	if err != nil {
		return nil, fmt.Errorf("perforce: open ticket: %w", err)
	}
	return h.swarmClientFromConfig(perforce.Config{
		BaseURL: conn.SwarmUrl,
		User:    conn.SwarmUser,
		Secret:  string(ticket),
	}), nil
}

// swarmClientFromConfig applies the default HTTP client factory unless a test
// has injected one.
func (h *Handler) swarmClientFromConfig(cfg perforce.Config) perforce.Client {
	if h.NewSwarmClient != nil {
		return h.NewSwarmClient(cfg)
	}
	return perforce.NewHTTPClient(cfg)
}

// workspacePerforceEnabled reports whether the workspace has opted into
// Perforce. Unlike GitHub, Perforce is opt-in: it returns false unless
// `perforce_enabled` is explicitly true, so a workspace that never configures
// Perforce is never polled.
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
// connection. The ticket is never returned — only has_credential.
type PerforceConnectionResponse struct {
	WorkspaceID   string  `json:"workspace_id"`
	SwarmURL      string  `json:"swarm_url"`
	SwarmUser     string  `json:"swarm_user"`
	HasCredential bool    `json:"has_credential"`
	LastPolledAt  *string `json:"last_polled_at,omitempty"`
}

func perforceConnectionToResponse(c db.PerforceConnection) PerforceConnectionResponse {
	resp := PerforceConnectionResponse{
		WorkspaceID:   uuidToString(c.WorkspaceID),
		SwarmURL:      c.SwarmUrl,
		SwarmUser:     c.SwarmUser,
		HasCredential: len(c.SwarmTicketEncrypted) > 0,
	}
	if c.LastPolledAt.Valid {
		s := c.LastPolledAt.Time.UTC().Format(time.RFC3339)
		resp.LastPolledAt = &s
	}
	return resp
}

// GetPerforceConnection (member) returns the workspace's Swarm connection, or a
// null connection when none is configured. configured reflects whether the
// at-rest key is set; can_manage gates the settings UI's edit controls.
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
		"configured": h.PerforceBox != nil,
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
	SwarmURL  string `json:"swarm_url"`
	SwarmUser string `json:"swarm_user"`
	// Ticket is write-only: a P4 password or ticket. Omitted when only the URL/
	// user changes, in which case the stored credential is preserved.
	Ticket string `json:"ticket"`
}

// SavePerforceConnection (admin) upserts the workspace's Swarm connection. The
// ticket is sealed at rest; omitting it on an existing connection preserves the
// stored secret.
func (h *Handler) SavePerforceConnection(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	if h.PerforceBox == nil {
		writeError(w, http.StatusServiceUnavailable, "perforce integration not configured (MULTICA_PERFORCE_SECRET_KEY unset)")
		return
	}
	var req perforceConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.SwarmURL = strings.TrimSpace(req.SwarmURL)
	req.SwarmUser = strings.TrimSpace(req.SwarmUser)
	if req.SwarmURL == "" || req.SwarmUser == "" {
		writeError(w, http.StatusBadRequest, "swarm_url and swarm_user are required")
		return
	}
	if !strings.HasPrefix(req.SwarmURL, "http://") && !strings.HasPrefix(req.SwarmURL, "https://") {
		writeError(w, http.StatusBadRequest, "swarm_url must start with http:// or https://")
		return
	}

	_, getErr := h.Queries.GetPerforceConnectionByWorkspace(r.Context(), wsUUID)
	exists := getErr == nil
	if !exists && !errors.Is(getErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load perforce connection")
		return
	}

	var sealed []byte
	if req.Ticket != "" {
		s, err := h.PerforceBox.Seal([]byte(req.Ticket))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store credential")
			return
		}
		sealed = s
	} else if !exists {
		writeError(w, http.StatusBadRequest, "ticket is required when creating a connection")
		return
	}

	member, _ := middleware.MemberFromContext(r.Context())
	conn, err := h.Queries.UpsertPerforceConnection(r.Context(), db.UpsertPerforceConnectionParams{
		WorkspaceID:          wsUUID,
		SwarmUrl:             req.SwarmURL,
		SwarmUser:            req.SwarmUser,
		SwarmTicketEncrypted: sealed,
		ConnectedByID:        member.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save perforce connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": perforceConnectionToResponse(conn)})
}

// DeletePerforceConnection (admin) removes the workspace's Swarm connection.
// Linked review history is left intact.
func (h *Handler) DeletePerforceConnection(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	conn, err := h.Queries.GetPerforceConnectionByWorkspace(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load perforce connection")
		return
	}
	if err := h.Queries.DeletePerforceConnection(r.Context(), db.DeletePerforceConnectionParams{
		ID:          conn.ID,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete perforce connection")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestPerforceConnection (admin) verifies credentials against Swarm with a
// minimal call. Credentials may come from the request body (test-before-save)
// or, when the ticket is omitted, from the stored connection.
func (h *Handler) TestPerforceConnection(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	if h.PerforceBox == nil {
		writeError(w, http.StatusServiceUnavailable, "perforce integration not configured")
		return
	}
	var req perforceConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cfg := perforce.Config{
		BaseURL: strings.TrimSpace(req.SwarmURL),
		User:    strings.TrimSpace(req.SwarmUser),
		Secret:  req.Ticket,
	}
	if cfg.Secret == "" || cfg.BaseURL == "" || cfg.User == "" {
		// Fall back to the stored connection for any omitted field.
		conn, err := h.Queries.GetPerforceConnectionByWorkspace(r.Context(), wsUUID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "swarm_url, swarm_user and ticket are required (no stored connection to fall back to)")
			return
		}
		if cfg.BaseURL == "" {
			cfg.BaseURL = conn.SwarmUrl
		}
		if cfg.User == "" {
			cfg.User = conn.SwarmUser
		}
		if cfg.Secret == "" {
			ticket, err := h.PerforceBox.Open(conn.SwarmTicketEncrypted)
			if err != nil {
				writeError(w, http.StatusBadRequest, "ticket is required")
				return
			}
			cfg.Secret = string(ticket)
		}
	}

	client := h.swarmClientFromConfig(cfg)
	if _, _, err := client.ListReviewsPage(r.Context(), 0, 1); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "could not reach Swarm or authentication failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
