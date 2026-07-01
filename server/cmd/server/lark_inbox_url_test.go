package main

import "testing"

func TestLarkInboxURLFromEnvFallsBackToAppURL(t *testing.T) {
	t.Setenv("MULTICA_PUBLIC_URL", "")
	t.Setenv("MULTICA_APP_URL", "https://multica-test.lilithgames.com/")
	t.Setenv("FRONTEND_ORIGIN", "")

	if got, want := larkInboxURLFromEnv(), "https://multica-test.lilithgames.com"; got != want {
		t.Fatalf("larkInboxURLFromEnv() = %q, want %q", got, want)
	}
}

func TestLarkInboxURLFromEnvFallsBackToPublicURL(t *testing.T) {
	t.Setenv("MULTICA_PUBLIC_URL", "https://api.example.com/")
	t.Setenv("MULTICA_APP_URL", "")
	t.Setenv("FRONTEND_ORIGIN", "")

	if got, want := larkInboxURLFromEnv(), "https://api.example.com"; got != want {
		t.Fatalf("larkInboxURLFromEnv() = %q, want %q", got, want)
	}
}

func TestLarkInboxURLFromEnvPrefersAppURL(t *testing.T) {
	t.Setenv("MULTICA_PUBLIC_URL", "https://api.example.com/")
	t.Setenv("MULTICA_APP_URL", "https://app.example.com/")
	t.Setenv("FRONTEND_ORIGIN", "")

	if got, want := larkInboxURLFromEnv(), "https://app.example.com"; got != want {
		t.Fatalf("larkInboxURLFromEnv() = %q, want %q", got, want)
	}
}
