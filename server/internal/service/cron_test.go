package service

import (
	"testing"
	"time"
)

func TestComputeNextRunAfterUsesProvidedBase(t *testing.T) {
	base := time.Date(2099, 1, 2, 10, 0, 0, 0, time.UTC)

	next, err := ComputeNextRunAfter("0 10 * * *", "UTC", base)
	if err != nil {
		t.Fatalf("ComputeNextRunAfter: %v", err)
	}

	want := time.Date(2099, 1, 3, 10, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("expected next run %s, got %s", want, next)
	}
}
