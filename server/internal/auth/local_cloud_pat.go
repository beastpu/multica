package auth

import (
	"context"
)

// LocalCloudPATVerifier resolves mcn_ PATs against the local cloud_node_token
// table instead of the remote Multica Cloud Fleet. Used when the in-process
// k8s fleet (kubefleet) is the node authority: it mints mcn_ tokens at node
// creation and deletes the rows at node deletion, so a plain DB lookup is the
// whole verification.
//
// Lookup is a closure over the sqlc queries (wired in cmd/server/router.go) so
// this package stays free of a pkg/db dependency, mirroring OwnerLookupFunc.
// It must return ErrCloudPATInvalid (or a CloudPATInvalidError) when the hash
// is unknown or expired, and any other error for infrastructure failures —
// the middlewares map those to 401 and 503 respectively.
//
// No Redis cache: the lookup is a single unique-index SELECT, and skipping the
// cache means node deletion revokes the token on the very next request.
type LocalCloudPATVerifier struct {
	Lookup func(ctx context.Context, tokenHash string) (CloudPATIdentity, error)
}

// Verify implements the same contract as CloudPATVerifier.Verify. The owner
// lookup is skipped: cloud_node_token.owner_id is a foreign key onto "user",
// so a returned identity always maps to a real local user.
func (v *LocalCloudPATVerifier) Verify(ctx context.Context, token string, _ OwnerLookupFunc) (CloudPATIdentity, error) {
	if v == nil || v.Lookup == nil {
		return CloudPATIdentity{}, ErrCloudPATNotConfigured
	}
	if token == "" {
		return CloudPATIdentity{}, ErrCloudPATInvalid
	}
	return v.Lookup(ctx, HashToken(token))
}
