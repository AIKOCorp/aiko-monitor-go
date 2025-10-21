package nethttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
	"github.com/aikocorp/aiko-monitor-go/internal/testserver"
	"github.com/aikocorp/aiko-monitor-go/nethttp"
)

const (
	projectKey = "pk_92Yb_kCIwRhy06UF-FQShg"
	secretKey  = "aNlvpEIXkeEubNgikWXyGnh8LyXa72yZhR9lEmzgHCM"
)

func shutdown(t *testing.T, monitor *aiko.Monitor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := monitor.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown monitor: %v", err)
	}
}

func TestNetHTTPMiddlewareRecordsRequests(t *testing.T) {
	server, err := testserver.StartMockServer(secretKey, projectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: projectKey,
		SecretKey:  secretKey,
		Endpoint:   server.Endpoint(),
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdown(t, monitor)

	handler := nethttp.Middleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/test?foo=1", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	event, err := server.WaitForEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}

	if event.Method != "GET" {
		t.Fatalf("expected method GET, got %s", event.Method)
	}
	if event.Endpoint != "/test" {
		t.Fatalf("expected endpoint /test, got %s", event.Endpoint)
	}
	if event.URL != "/test?foo=1" {
		t.Fatalf("expected url /test?foo=1, got %s", event.URL)
	}
	if ct := event.ResponseHeaders["content-type"]; !strings.Contains(ct, "application/json") {
		t.Fatalf("expected content-type header, got %s", ct)
	}
}

func TestNetHTTPMiddlewareSynthesizesErrorBody(t *testing.T) {
	server, err := testserver.StartMockServer(secretKey, projectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: projectKey,
		SecretKey:  secretKey,
		Endpoint:   server.Endpoint(),
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdown(t, monitor)

	handler := nethttp.Middleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/oops", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 response, got %d", resp.Code)
	}

	event, err := server.WaitForEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}

	if event.Endpoint != "/oops" {
		t.Fatalf("expected endpoint /oops, got %s", event.Endpoint)
	}
	if event.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", event.StatusCode)
	}

	body, ok := event.ResponseBody.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map response body, got %T", event.ResponseBody)
	}
	if message, ok := body["error"].(string); !ok || message != "Internal Server Error" {
		t.Fatalf("expected synthesized error body, got %#v", event.ResponseBody)
	}
}
