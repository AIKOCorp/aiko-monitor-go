package aiko_test

import (
	"testing"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
)

const (
	validProjectKey = "pk_AAAAAAAAAAAAAAAAAAAAAA"
	validSecretKey  = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func TestValidateConfigAcceptsProductionEndpoints(t *testing.T) {
	if err := aiko.ValidateConfig(validProjectKey, validSecretKey, "https://monitor.aikocorp.ai/api/ingest"); err != nil {
		t.Fatalf("main endpoint should be accepted: %v", err)
	}
	if err := aiko.ValidateConfig(validProjectKey, validSecretKey, "https://staging.aikocorp.ai/api/monitor/ingest"); err != nil {
		t.Fatalf("staging endpoint should be accepted: %v", err)
	}
}

func TestValidateConfigAllowsLocalhostEndpoints(t *testing.T) {
	cases := []string{
		"http://localhost:8080/api/monitor/ingest",
		"http://127.0.0.1:9000/api/monitor/ingest",
		"http://[::1]:3000/api/monitor/ingest",
	}

	for _, endpoint := range cases {
		if err := aiko.ValidateConfig(validProjectKey, validSecretKey, endpoint); err != nil {
			t.Fatalf("endpoint %q should be accepted: %v", endpoint, err)
		}
	}
}

func TestValidateConfigRejectsInvalidProjectKey(t *testing.T) {
	err := aiko.ValidateConfig("bad", validSecretKey, "https://monitor.aikocorp.ai/api/ingest")
	if err == nil {
		t.Fatal("expected project key error")
	}
	expected := "projectKey must start with 'pk_' followed by 22 base64url characters"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfigRejectsInvalidSecretLength(t *testing.T) {
	err := aiko.ValidateConfig(validProjectKey, "short", "https://monitor.aikocorp.ai/api/ingest")
	if err == nil {
		t.Fatal("expected secret key error")
	}
	expected := "secretKey must be exactly 43 base64url characters"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfigRejectsInvalidEndpoint(t *testing.T) {
	err := aiko.ValidateConfig(validProjectKey, validSecretKey, "https://example.com")
	if err == nil {
		t.Fatal("expected endpoint error")
	}
	expected := "endpoint must match http://localhost:PORT/api/monitor/ingest or be 'https://monitor.aikocorp.ai/api/ingest' or 'https://staging.aikocorp.ai/api/monitor/ingest'"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}
