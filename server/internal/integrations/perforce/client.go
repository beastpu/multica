// Package perforce holds the workspace-agnostic projection of a Helix Swarm
// review. v1 is webhook-push only: the Swarm platform sends a full review
// snapshot to Multica, which maps it onto this struct. Multica never calls back
// to Swarm (it is on the internal network), so there is no HTTP client here.
package perforce

import "time"

// Review is the projection of a Swarm review that Multica persists. review_id is
// the stable spine across shelve iterations and through submit; ShelvedCL is the
// pending changelist under review and CommittedCL is the (renumbered) submitted
// changelist once the review lands.
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
}
