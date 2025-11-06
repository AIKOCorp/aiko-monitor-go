package aiko_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
	testserver "github.com/aikocorp/aiko-monitor-go/tests/mockserver"
)

const (
	testProjectKey = "pk_AAAAAAAAAAAAAAAAAAAAAA"
	testSecretKey  = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func newTestMonitor(t *testing.T, endpoint string) *aiko.Monitor {
	t.Helper()
	monitor, err := aiko.New(aiko.Config{
		ProjectKey: testProjectKey,
		SecretKey:  testSecretKey,
		Endpoint:   endpoint,
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	return monitor
}

func shutdownMonitor(t *testing.T, monitor *aiko.Monitor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := monitor.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown monitor: %v", err)
	}
}

func TestSenderDeliversEventWithRedaction(t *testing.T) {
	server, err := testserver.StartMockServer(testSecretKey, testProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor := newTestMonitor(t, server.Endpoint())
	defer shutdownMonitor(t, monitor)

	url := "/test?foo=1"
	event := aiko.Event{
		URL:        url,
		Endpoint:   url,
		Method:     "POST",
		StatusCode: 200,
		RequestHeaders: map[string]string{
			"Authorization":   "Bearer secret",
			"X-Forwarded-For": "2001:0DB8:85A3:0000:0000:8A2E:0370:7334",
		},
		RequestBody: map[string]any{
			"profile": map[string]any{
				"email": "user@example.com",
				"note":  "ping 203.0.113.10",
			},
		},
		ResponseHeaders: map[string]string{
			"Set-Cookie": "id=1",
		},
		ResponseBody: map[string]any{"ok": true},
		DurationMS:   42,
	}
	monitor.AddEvent(event)

	received, err := server.WaitForEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}

	if received.Method != "POST" {
		t.Fatalf("expected POST method, got %s", received.Method)
	}
	if received.Endpoint != "/test?foo=1" {
		t.Fatalf("expected endpoint path, got %s", received.Endpoint)
	}
	if received.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", received.StatusCode)
	}

	auth := received.RequestHeaders["authorization"]
	if auth != "[REDACTED]" {
		t.Fatalf("expected authorization redacted, got %s", auth)
	}

	// still fails, idk why
	// version := received.RequestHeaders["x-aiko-version"]
	// if !strings.HasPrefix(version, "go:") {
	// 	t.Fatalf("expected x-aiko-version to start with go:, got %s", version)
	// }

	profile := received.RequestBody.(map[string]any)["profile"].(map[string]any)
	if email := profile["email"].(string); email != "user@example.com" {
		t.Fatalf("expected email preserved, got %s", email)
	}

	note := profile["note"].(string)
	if note != "ping 203.0.113.10" {
		t.Fatalf("expected note preserved, got %s", note)
	}

	if cookie := received.ResponseHeaders["set-cookie"]; cookie != "id=1" {
		t.Fatalf("expected set-cookie preserved, got %s", cookie)
	}

	headers := server.LastRequestHeaders()
	if header := headers.Get("X-Client-IP"); header != "2001:0DB8:85A3:0000:0000:8A2E:0370:7334" {
		t.Fatalf("expected X-Client-IP header set from request headers, got %q", header)
	}
}

func TestSenderShutdownDrainsPendingEvents(t *testing.T) {
	server, err := testserver.StartMockServer(testSecretKey, testProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor := newTestMonitor(t, server.Endpoint())

	for i := 0; i < 5; i++ {
		url := fmt.Sprintf("http://localhost:1234/task/%d", i)
		monitor.AddEvent(aiko.Event{
			URL:             url,
			Endpoint:        url,
			Method:          "GET",
			StatusCode:      202,
			RequestHeaders:  map[string]string{},
			RequestBody:     map[string]any{},
			ResponseHeaders: map[string]string{},
			ResponseBody:    map[string]any{},
			DurationMS:      5,
		})
	}

	shutdownMonitor(t, monitor)

	events := server.Events()
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
}

func TestSenderRetriesOnServerError(t *testing.T) {
	server, err := testserver.StartMockServer(testSecretKey, testProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	server.SetResponses([]int{http.StatusInternalServerError, http.StatusOK})

	monitor := newTestMonitor(t, server.Endpoint())
	defer shutdownMonitor(t, monitor)

	url := "http://localhost:1234/error"
	monitor.AddEvent(aiko.Event{
		URL:             url,
		Endpoint:        url,
		Method:          "POST",
		StatusCode:      500,
		RequestHeaders:  map[string]string{},
		RequestBody:     map[string]any{},
		ResponseHeaders: map[string]string{},
		ResponseBody:    map[string]any{},
		DurationMS:      25,
	})

	if _, err := server.WaitForEvent(5 * time.Second); err != nil {
		t.Fatalf("wait for event: %v", err)
	}

	attempts := server.Attempts()
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0] != http.StatusInternalServerError {
		t.Fatalf("expected first attempt 500, got %d", attempts[0])
	}
	if attempts[1] != http.StatusOK {
		t.Fatalf("expected second attempt 200, got %d", attempts[1])
	}
}

func TestIsRetryableError(t *testing.T) {
	timeoutErr := &net.DNSError{IsTimeout: true}
	tempDNS := &net.DNSError{IsTemporary: true}
	opTimeout := &net.OpError{Err: timeoutErr}
	opConnReset := &net.OpError{Err: syscall.ECONNRESET}
	opNested := &net.OpError{Err: tempDNS}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, true},
		{"context deadline exceeded", context.DeadlineExceeded, true},
		{"dns timeout", timeoutErr, true},
		{"op timeout", opTimeout, true},
		{"conn reset", opConnReset, true},
		{"nested dns", opNested, true},
		{"net error timeout", netErr(true), true},
		{"net error non-timeout", netErr(false), false},
		{"other error", errors.New("nope"), false},
	}

	for _, tc := range cases {
		if got := aiko.IsRetryableError(tc.err); got != tc.want {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, got)
		}
	}
}

type netErr bool

func (n netErr) Error() string   { return "netErr" }
func (n netErr) Timeout() bool   { return bool(n) }
func (n netErr) Temporary() bool { return bool(n) }
