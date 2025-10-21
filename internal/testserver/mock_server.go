// Package testserver provides a mock ingest endpoint for integration tests.
package testserver

import (
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
)

// MockServer emulates the ingest endpoint used by tests.
type MockServer struct {
	projectKey string
	secret     []byte

	srv *httptest.Server

	mu        sync.Mutex
	events    []aiko.Event
	attempts  []int
	responses []int

	eventCh chan aiko.Event
}

// StartMockServer boots a mock ingest server with signature validation.
func StartMockServer(secretKey, projectKey string) (*MockServer, error) {
	secret, err := base64.RawURLEncoding.DecodeString(secretKey)
	if err != nil {
		return nil, fmt.Errorf("decode secret: %w", err)
	}

	ms := &MockServer{
		projectKey: projectKey,
		secret:     secret,
		eventCh:    make(chan aiko.Event, 100),
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen tcp4: %w", err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(ms.handle))
	server.Listener = listener
	server.Start()

	ms.srv = server
	return ms, nil
}

func (m *MockServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Path != "/api/monitor/ingest" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	if r.Header.Get("X-Project-Key") != m.projectKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	expectedSig := hex.EncodeToString(sign(m.secret, body))
	if !hmac.Equal([]byte(r.Header.Get("X-Signature")), []byte(expectedSig)) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	event, err := decodeEvent(body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	status := http.StatusOK
	if len(m.responses) > 0 {
		status = m.responses[0]
		m.responses = m.responses[1:]
	}
	m.attempts = append(m.attempts, status)
	if status >= 200 && status < 300 {
		m.events = append(m.events, event)
		select {
		case m.eventCh <- event:
		default:
		}
	}
	m.mu.Unlock()

	w.WriteHeader(status)
}

func decodeEvent(body []byte) (aiko.Event, error) {
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return aiko.Event{}, err
	}
	defer gr.Close()

	payload, err := io.ReadAll(gr)
	if err != nil {
		return aiko.Event{}, err
	}

	var evt aiko.Event
	if err := json.Unmarshal(payload, &evt); err != nil {
		return aiko.Event{}, err
	}
	return evt, nil
}

func sign(secret, body []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return mac.Sum(nil)
}

// Endpoint returns the ingest endpoint URL for the mock server.
func (m *MockServer) Endpoint() string {
	return m.srv.URL + "/api/monitor/ingest"
}

// SetResponses configures the sequence of HTTP statuses the mock server should emit.
func (m *MockServer) SetResponses(statuses []int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append([]int(nil), statuses...)
}

// Events returns a snapshot of all received events.
func (m *MockServer) Events() []aiko.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]aiko.Event, len(m.events))
	copy(out, m.events)
	return out
}

// Attempts returns the recorded response statuses emitted by the server.
func (m *MockServer) Attempts() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, len(m.attempts))
	copy(out, m.attempts)
	return out
}

// WaitForEvent blocks until an event is received or the timeout elapses.
func (m *MockServer) WaitForEvent(timeout time.Duration) (aiko.Event, error) {
	select {
	case evt := <-m.eventCh:
		return evt, nil
	case <-time.After(timeout):
		return aiko.Event{}, fmt.Errorf("timeout waiting for event")
	}
}

// Stop shuts down the server and releases resources.
func (m *MockServer) Stop() {
	if m == nil || m.srv == nil {
		return
	}
	m.srv.Close()
	close(m.eventCh)
}
