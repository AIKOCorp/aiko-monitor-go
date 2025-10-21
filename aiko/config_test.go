package aiko

import "testing"

const (
	validProjectKey = "pk_92Yb_kCIwRhy06UF-FQShg"
	validSecretKey  = "aNlvpEIXkeEubNgikWXyGnh8LyXa72yZhR9lEmzgHCM"
)

func TestValidateConfigAcceptsProductionEndpoints(t *testing.T) {
	if err := validateConfig(validProjectKey, validSecretKey, defaultEndpoint); err != nil {
		t.Fatalf("main endpoint should be accepted: %v", err)
	}
	if err := validateConfig(validProjectKey, validSecretKey, stagingEndpoint); err != nil {
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
		if err := validateConfig(validProjectKey, validSecretKey, endpoint); err != nil {
			t.Fatalf("endpoint %q should be accepted: %v", endpoint, err)
		}
	}
}

func TestValidateConfigRejectsInvalidProjectKey(t *testing.T) {
	err := validateConfig("bad", validSecretKey, defaultEndpoint)
	if err == nil {
		t.Fatal("expected project key error")
	}
	expected := "projectKey must start with 'pk_' followed by 22 base64url characters"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfigRejectsInvalidSecretLength(t *testing.T) {
	err := validateConfig(validProjectKey, "short", defaultEndpoint)
	if err == nil {
		t.Fatal("expected secret key error")
	}
	expected := "secretKey must be exactly 43 base64url characters"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfigRejectsInvalidEndpoint(t *testing.T) {
	err := validateConfig(validProjectKey, validSecretKey, "https://example.com")
	if err == nil {
		t.Fatal("expected endpoint error")
	}
	expected := "endpoint must match http://localhost:PORT/api/monitor/ingest or be 'https://main.aikocorp.ai/api/monitor/ingest' or 'https://staging.aikocorp.ai/api/monitor/ingest'"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}
