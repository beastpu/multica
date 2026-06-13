package perforce

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseReviews(t *testing.T) {
	body := []byte(`{
		"error": null,
		"messages": [],
		"data": {
			"totalCount": 2,
			"lastSeen": 41,
			"reviews": [
				{
					"id": 42,
					"state": "needsReview",
					"description": "Fix airdrop crash\n\nFixes MUL-123",
					"author": "alice",
					"changes": [1001, 1005],
					"commits": [],
					"created": 1700000000,
					"updated": 1700000500
				},
				{
					"id": 41,
					"state": "approved",
					"description": "Resolves MUL-99",
					"author": "bob",
					"changes": [900, 950],
					"commits": [950],
					"created": 1699990000,
					"updated": 1699990800
				}
			]
		}
	}`)

	reviews, lastSeen, err := parseReviews(body)
	if err != nil {
		t.Fatalf("parseReviews: %v", err)
	}
	if lastSeen != 41 {
		t.Fatalf("lastSeen = %d, want 41", lastSeen)
	}
	if len(reviews) != 2 {
		t.Fatalf("got %d reviews, want 2", len(reviews))
	}

	r0 := reviews[0]
	if r0.ID != 42 || r0.State != "needsReview" || r0.Author != "alice" {
		t.Errorf("r0 basic fields wrong: %+v", r0)
	}
	if r0.Title != "Fix airdrop crash" {
		t.Errorf("r0.Title = %q, want first line", r0.Title)
	}
	if r0.CommittedCL != nil {
		t.Errorf("r0.CommittedCL = %v, want nil (not committed)", *r0.CommittedCL)
	}
	if r0.ShelvedCL == nil || *r0.ShelvedCL != 1005 {
		t.Errorf("r0.ShelvedCL = %v, want 1005 (latest uncommitted change)", r0.ShelvedCL)
	}

	r1 := reviews[1]
	if r1.CommittedCL == nil || *r1.CommittedCL != 950 {
		t.Errorf("r1.CommittedCL = %v, want 950", r1.CommittedCL)
	}
	if r1.ShelvedCL == nil || *r1.ShelvedCL != 900 {
		t.Errorf("r1.ShelvedCL = %v, want 900 (latest change not in commits)", r1.ShelvedCL)
	}
}

func TestParseReviewsMalformed(t *testing.T) {
	if _, _, err := parseReviews([]byte(`{not json`)); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

func TestListReviewsPageSendsAuthAndCursor(t *testing.T) {
	var gotUser, gotPass, gotAfter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		gotAfter = r.URL.Query().Get("after")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"lastSeen":1,"reviews":[
			{"id": 3, "state": "needsReview", "description": "c", "changes": [30], "updated": 300, "created": 30},
			{"id": 2, "state": "approved", "description": "b", "changes": [20], "updated": 200, "created": 20}
		]}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(Config{BaseURL: srv.URL, User: "svc", Secret: "ticket123"})
	got, cursor, err := c.ListReviewsPage(context.Background(), 5, 50)
	if err != nil {
		t.Fatalf("ListReviewsPage: %v", err)
	}
	if gotUser != "svc" || gotPass != "ticket123" {
		t.Errorf("basic auth not sent: user=%q", gotUser)
	}
	if gotAfter != "5" {
		t.Errorf("after cursor = %q, want 5", gotAfter)
	}
	if len(got) != 2 || cursor != 1 {
		t.Fatalf("got %d reviews, cursor %d; want 2, 1", len(got), cursor)
	}
	if got[0].ID != 3 {
		t.Errorf("expected id-descending order, got first id %d", got[0].ID)
	}
}

func TestGetReviewNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not Found"}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(Config{BaseURL: srv.URL, User: "svc", Secret: "x"})
	_, found, err := c.GetReview(context.Background(), 999)
	if err != nil {
		t.Fatalf("GetReview: %v", err)
	}
	if found {
		t.Error("expected found=false on 404")
	}
}
