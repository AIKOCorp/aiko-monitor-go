package aiko

import "testing"

func TestValidateConfig(t *testing.T) {
	validSecret := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := validateConfig("pk_abcdefghijklmnopqrstuv", validSecret, defaultEndpoint); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}

	t.Run("invalid project key", func(t *testing.T) {
		if err := validateConfig("not-a-key", validSecret, defaultEndpoint); err == nil {
			t.Fatal("expected project key error")
		}
	})

	t.Run("invalid secret length", func(t *testing.T) {
		if err := validateConfig("pk_abcdefghijklmnopqrstuv", "short", defaultEndpoint); err == nil {
			t.Fatal("expected secret key error")
		}
	})

	t.Run("invalid endpoint", func(t *testing.T) {
		if err := validateConfig("pk_abcdefghijklmnopqrstuv", validSecret, "https://example.com"); err == nil {
			t.Fatal("expected endpoint error")
		}
	})

	t.Run("local endpoint", func(t *testing.T) {
		local := "http://localhost:8080/api/monitor/ingest"
		if err := validateConfig("pk_abcdefghijklmnopqrstuv", validSecret, local); err != nil {
			t.Fatalf("expected local endpoint to be allowed, got %v", err)
		}
	})
}
