// Package perforce wraps the Helix Swarm REST API behind a small interface
// so the review poller can be tested with a fake client (go-backend-quality:
// External Integrations). It speaks only to Swarm over HTTP — it never talks
// to p4d directly. Swarm authenticates against Perforce user accounts, so the
// credential is a P4 username plus a ticket or password used as HTTP Basic
// auth.
package perforce

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Review is the workspace-agnostic projection of a Swarm review that the
// poller persists. review_id is the stable spine across shelve iterations and
// through submit; ShelvedCL is the pending changelist under review and
// CommittedCL is the (renumbered) submitted changelist once the review lands.
type Review struct {
	ID          int64
	State       string // raw Swarm state: needsReview|needsRevision|approved|rejected|archived
	Title       string
	Author      string
	Description string // carries issue identifiers, e.g. "Fixes MUL-123"
	ShelvedCL   *int64
	CommittedCL *int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// Updated is the raw Swarm `updated` epoch (seconds). The poller stores the
	// max seen value as its watermark, so it is kept separate from the parsed
	// UpdatedAt to avoid any timezone/rounding drift in the cursor.
	Updated int64
}

// Client is the Swarm API surface the poller needs. Swarm lists reviews by id
// descending, so discovery pages newest-first via ListReviewsPage and progress
// is tracked by re-fetching known in-flight reviews with GetReview.
type Client interface {
	// ListReviewsPage returns one page of reviews ordered by id descending,
	// starting after afterID (0 = newest). nextCursor is the Swarm lastSeen id
	// to pass as afterID for the next (older) page, or 0 when exhausted.
	ListReviewsPage(ctx context.Context, afterID int64, max int) (reviews []Review, nextCursor int64, err error)
	// GetReview fetches a single review by id. found is false on 404.
	GetReview(ctx context.Context, id int64) (review Review, found bool, err error)
}

// Config configures an HTTPClient.
type Config struct {
	// BaseURL is the Swarm base URL including scheme and optional port,
	// e.g. "http://igame-swarm.lilithgame.com".
	BaseURL string
	// User is the Perforce/Swarm account used for HTTP Basic auth.
	User string
	// Secret is the P4 ticket or password for that account.
	Secret string
	// HTTPClient is optional; a 30s-timeout client is used when nil.
	HTTPClient *http.Client
	// MaxReviewsPerPoll caps how many reviews a single poll pulls, bounding
	// work per scheduler tick. Defaults to 200 when <= 0.
	MaxReviewsPerPoll int
}

// HTTPClient is the production Client backed by the Swarm REST API.
type HTTPClient struct {
	baseURL string
	user    string
	secret  string
	http    *http.Client
	maxPoll int
}

// NewHTTPClient builds an HTTPClient. BaseURL is normalized to drop a trailing
// slash so request URLs join cleanly.
func NewHTTPClient(cfg Config) *HTTPClient {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	maxPoll := cfg.MaxReviewsPerPoll
	if maxPoll <= 0 {
		maxPoll = 200
	}
	return &HTTPClient{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		user:    cfg.User,
		secret:  cfg.Secret,
		http:    hc,
		maxPoll: maxPoll,
	}
}

// swarmReviewsResponse mirrors the Swarm v10 GET /api/v10/reviews payload as
// returned by igame-swarm (SWARM/2025.2). The reviews and pagination cursor are
// nested under `data`; lastSeen is a review id cursor (list is id-descending).
type swarmReviewsResponse struct {
	Data struct {
		Reviews  []swarmReview `json:"reviews"`
		LastSeen int64         `json:"lastSeen"`
	} `json:"data"`
}

type swarmReview struct {
	ID          int64   `json:"id"`
	State       string  `json:"state"`
	Description string  `json:"description"`
	Author      string  `json:"author"`
	Changes     []int64 `json:"changes"` // all changelists associated with the review
	Commits     []int64 `json:"commits"` // committed (submitted) changelists
	Created     int64   `json:"created"` // epoch seconds
	Updated     int64   `json:"updated"` // epoch seconds
}

const reviewFields = "id,state,description,author,changes,commits,created,updated"

// ListReviewsPage returns one id-descending page after afterID. The caller
// pages older by passing the returned nextCursor until it reaches its
// discovery watermark.
func (c *HTTPClient) ListReviewsPage(ctx context.Context, afterID int64, max int) ([]Review, int64, error) {
	if max <= 0 || max > c.maxPoll {
		max = c.maxPoll
	}
	q := url.Values{}
	q.Set("max", strconv.Itoa(max))
	q.Set("fields", reviewFields)
	if afterID > 0 {
		q.Set("after", strconv.FormatInt(afterID, 10))
	}
	body, status, err := c.doGet(ctx, "/api/v10/reviews?"+q.Encode())
	if err != nil {
		return nil, 0, err
	}
	if status != http.StatusOK {
		return nil, 0, fmt.Errorf("swarm: list reviews: unexpected status %d", status)
	}
	return parseReviews(body)
}

// GetReview fetches a single review by id for progress tracking.
func (c *HTTPClient) GetReview(ctx context.Context, id int64) (Review, bool, error) {
	q := url.Values{}
	q.Set("fields", reviewFields)
	body, status, err := c.doGet(ctx, "/api/v10/reviews/"+strconv.FormatInt(id, 10)+"?"+q.Encode())
	if err != nil {
		return Review{}, false, err
	}
	if status == http.StatusNotFound {
		return Review{}, false, nil
	}
	if status != http.StatusOK {
		return Review{}, false, fmt.Errorf("swarm: get review %d: unexpected status %d", id, status)
	}
	reviews, _, err := parseReviews(body)
	if err != nil {
		return Review{}, false, err
	}
	if len(reviews) == 0 {
		return Review{}, false, nil
	}
	return reviews[0], true, nil
}

// doGet issues an authenticated GET and returns the body and status. The body
// is never logged — it may echo auth context.
func (c *HTTPClient) doGet(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth(c.user, c.secret)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("swarm: request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("swarm: read body: %w", err)
	}
	return body, resp.StatusCode, nil
}

// parseReviews maps a raw Swarm payload to domain Reviews and the lastSeen
// cursor. Isolated for table-driven unit tests against captured fixtures.
func parseReviews(body []byte) ([]Review, int64, error) {
	var raw swarmReviewsResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, fmt.Errorf("swarm: decode reviews: %w", err)
	}
	out := make([]Review, 0, len(raw.Data.Reviews))
	for _, r := range raw.Data.Reviews {
		out = append(out, mapReview(r))
	}
	return out, raw.Data.LastSeen, nil
}

func mapReview(r swarmReview) Review {
	rev := Review{
		ID:          r.ID,
		State:       r.State,
		Description: r.Description,
		Author:      r.Author,
		CreatedAt:   epochToTime(r.Created),
		UpdatedAt:   epochToTime(r.Updated),
		Updated:     r.Updated,
	}
	// Title is the first non-empty line of the description — Swarm has no
	// separate title field on a review.
	rev.Title = firstLine(r.Description)
	// Committed changelist: the latest entry in commits, once submitted.
	if cl, ok := lastOf(r.Commits); ok {
		rev.CommittedCL = &cl
	}
	// Shelved changelist: the latest associated change that is not the
	// committed one. Falls back to the latest change.
	if cl, ok := latestShelved(r.Changes, r.Commits); ok {
		rev.ShelvedCL = &cl
	}
	return rev
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func lastOf(xs []int64) (int64, bool) {
	if len(xs) == 0 {
		return 0, false
	}
	return xs[len(xs)-1], true
}

// latestShelved returns the most recent change that has not been committed.
func latestShelved(changes, commits []int64) (int64, bool) {
	committed := map[int64]struct{}{}
	for _, c := range commits {
		committed[c] = struct{}{}
	}
	for i := len(changes) - 1; i >= 0; i-- {
		if _, done := committed[changes[i]]; !done {
			return changes[i], true
		}
	}
	return lastOf(changes)
}

func epochToTime(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}
