package auth

import (
	"context"
	"errors"
	"testing"
)

func TestLocalCloudPATVerifier_Verify(t *testing.T) {
	const token = "mcn_deadbeef"
	wantHash := HashToken(token)

	v := &LocalCloudPATVerifier{
		Lookup: func(_ context.Context, tokenHash string) (CloudPATIdentity, error) {
			if tokenHash != wantHash {
				return CloudPATIdentity{}, ErrCloudPATInvalid
			}
			return CloudPATIdentity{OwnerID: "owner-1", InstanceID: "node-1"}, nil
		},
	}

	id, err := v.Verify(context.Background(), token, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.OwnerID != "owner-1" || id.InstanceID != "node-1" {
		t.Fatalf("identity = %+v", id)
	}

	if _, err := v.Verify(context.Background(), "mcn_other", nil); !errors.Is(err, ErrCloudPATInvalid) {
		t.Fatalf("unknown token: err = %v, want ErrCloudPATInvalid", err)
	}
	if _, err := v.Verify(context.Background(), "", nil); !errors.Is(err, ErrCloudPATInvalid) {
		t.Fatalf("empty token: err = %v, want ErrCloudPATInvalid", err)
	}
}

func TestLocalCloudPATVerifier_NilFailsClosed(t *testing.T) {
	var v *LocalCloudPATVerifier
	if _, err := v.Verify(context.Background(), "mcn_x", nil); !errors.Is(err, ErrCloudPATNotConfigured) {
		t.Fatalf("nil verifier: err = %v, want ErrCloudPATNotConfigured", err)
	}
	empty := &LocalCloudPATVerifier{}
	if _, err := empty.Verify(context.Background(), "mcn_x", nil); !errors.Is(err, ErrCloudPATNotConfigured) {
		t.Fatalf("verifier without lookup: err = %v, want ErrCloudPATNotConfigured", err)
	}
}

func TestGenerateCloudNodeToken_Prefix(t *testing.T) {
	tok, err := GenerateCloudNodeToken()
	if err != nil {
		t.Fatalf("GenerateCloudNodeToken: %v", err)
	}
	if len(tok) != len(CloudPATPrefix)+40 {
		t.Fatalf("token length = %d, want %d", len(tok), len(CloudPATPrefix)+40)
	}
	if tok[:len(CloudPATPrefix)] != CloudPATPrefix {
		t.Fatalf("token prefix = %q, want %q", tok[:len(CloudPATPrefix)], CloudPATPrefix)
	}
}
