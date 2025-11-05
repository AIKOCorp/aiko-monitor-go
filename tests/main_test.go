package aiko_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"iter"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
	testserver "github.com/aikocorp/aiko-monitor-go/tests/mockserver"
)

type hijackableResponseWriter struct {
	header   http.Header
	status   int
	flushed  bool
	hijacked bool
	pushed   []string

	conn net.Conn
	peer net.Conn
	rw   *bufio.ReadWriter
}

func newHijackableResponseWriter(t *testing.T) *hijackableResponseWriter {
	t.Helper()
	c1, c2 := net.Pipe()
	return &hijackableResponseWriter{
		header: make(http.Header),
		conn:   c1,
		peer:   c2,
		rw:     bufio.NewReadWriter(bufio.NewReader(c2), bufio.NewWriter(c2)),
	}
}

func (w *hijackableResponseWriter) Close()                      { _ = w.conn.Close(); _ = w.peer.Close() }
func (w *hijackableResponseWriter) Header() http.Header         { return w.header }
func (w *hijackableResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *hijackableResponseWriter) WriteHeader(status int)      { w.status = status }
func (w *hijackableResponseWriter) Flush()                      { w.flushed = true }
func (w *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return w.conn, w.rw, nil
}
func (w *hijackableResponseWriter) Push(target string, _ *http.PushOptions) error {
	w.pushed = append(w.pushed, target)
	return nil
}

var (
	_ http.Flusher  = (*hijackableResponseWriter)(nil)
	_ http.Hijacker = (*hijackableResponseWriter)(nil)
	_ http.Pusher   = (*hijackableResponseWriter)(nil)
)

const (
	middlewareProjectKey = "pk_AAAAAAAAAAAAAAAAAAAAAA"
	middlewareSecretKey  = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func shutdownMonitorHelper(t *testing.T, monitor *aiko.Monitor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := monitor.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown monitor: %v", err)
	}
}

func TestNetHTTPMiddlewareRecordsRequests(t *testing.T) {
	server, err := testserver.StartMockServer(middlewareSecretKey, middlewareProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Endpoint:   server.Endpoint(),
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownMonitorHelper(t, monitor)

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if event.Endpoint != "/test?foo=1" {
		t.Fatalf("expected endpoint /test, got %s", event.Endpoint)
	}
	if event.URL != "/test?foo=1" {
		t.Fatalf("expected url /test?foo=1, got %s", event.URL)
	}
	if ct := event.ResponseHeaders["content-type"]; !strings.Contains(ct, "application/json") {
		t.Fatalf("expected content-type header, got %s", ct)
	}
}

func TestNetHTTPMiddlewareSetsClientIPHeader(t *testing.T) {
	server, err := testserver.StartMockServer(middlewareSecretKey, middlewareProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Endpoint:   server.Endpoint(),
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownMonitorHelper(t, monitor)

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.RemoteAddr = "203.0.113.99:52345"
	req.Header.Set("CF-Connecting-IP", "198.51.100.25")
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 198.51.100.2")
	req.Header.Set("Forwarded", "for=203.0.113.200")

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if _, err := server.WaitForEvent(3 * time.Second); err != nil {
		t.Fatalf("wait for event: %v", err)
	}

	headers := server.LastRequestHeaders()
	if got := headers.Get("X-Client-IP"); got != "198.51.100.25" {
		t.Fatalf("expected X-Client-IP header 198.51.100.25, got %q", got)
	}
}

func TestNetHTTPMiddlewareSynthesizesErrorBody(t *testing.T) {
	server, err := testserver.StartMockServer(middlewareSecretKey, middlewareProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Endpoint:   server.Endpoint(),
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownMonitorHelper(t, monitor)

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	body, ok := event.ResponseBody.(map[string]any)
	if !ok {
		t.Fatalf("expected map response body, got %T", event.ResponseBody)
	}
	if message, ok := body["error"].(string); !ok || message != "Internal Server Error" {
		t.Fatalf("expected synthesized error body, got %#v", event.ResponseBody)
	}
}

func TestNetHTTPMiddlewarePropagatesPanicsAfterRecording(t *testing.T) {
	server, err := testserver.StartMockServer(middlewareSecretKey, middlewareProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Endpoint:   server.Endpoint(),
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownMonitorHelper(t, monitor)

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/panic", nil)
	resp := httptest.NewRecorder()

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("expected panic to propagate")
		}
	}()

	handler.ServeHTTP(resp, req)

	event, err := server.WaitForEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}
	if event.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 status, got %d", event.StatusCode)
	}
	body, ok := event.ResponseBody.(map[string]any)
	if !ok || body["error"] != "boom" {
		t.Fatalf("expected panic message in response body, got %#v", event.ResponseBody)
	}
	if header := event.RequestHeaders["x-aiko-version"]; !strings.HasPrefix(header, "go:") {
		t.Fatalf("expected x-aiko-version header, got %q", header)
	}
}

const (
	fastHTTPProjectKey = "pk_AAAAAAAAAAAAAAAAAAAAAA"
	fastHTTPSecretKey  = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func newFastHTTPMonitor(t *testing.T, endpoint string) *aiko.Monitor {
	t.Helper()
	monitor, err := aiko.New(aiko.Config{
		ProjectKey: fastHTTPProjectKey,
		SecretKey:  fastHTTPSecretKey,
		Endpoint:   endpoint,
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	return monitor
}

func shutdownFastHTTPMonitor(t *testing.T, monitor *aiko.Monitor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := monitor.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown monitor: %v", err)
	}
}

func prepareRequestCtx(method, uri string, body []byte) *fasthttp.RequestCtx {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(uri)
	req.Header.SetMethod(method)
	if body != nil {
		req.SetBody(body)
	}
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(req, nil, nil)
	fasthttp.ReleaseRequest(req)
	return ctx
}

func TestFastHTTPMiddlewareRecordsRequests(t *testing.T) {
	server, err := testserver.StartMockServer(fastHTTPSecretKey, fastHTTPProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor := newFastHTTPMonitor(t, server.Endpoint())
	defer shutdownFastHTTPMonitor(t, monitor)

	handler := aiko.FastHTTPMiddleware(monitor, func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Content-Type", "application/json")
		ctx.Response.Header.Add("Set-Cookie", "a=1")
		ctx.Response.Header.Add("Set-Cookie", "b=2")
		ctx.SetStatusCode(fasthttp.StatusAccepted)
		ctx.SetBody([]byte(`{"ok":true}`))
	})

	ctx := prepareRequestCtx(fasthttp.MethodPost, "/fast?x=1", []byte(`{"id":123}`))
	ctx.Request.Header.Add("Set-Cookie", "session=abc")

	handler(ctx)

	event, err := server.WaitForEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}
	if event.StatusCode != fasthttp.StatusAccepted {
		t.Fatalf("expected 202 status, got %d", event.StatusCode)
	}
	if event.RequestHeaders["x-aiko-version"] == "" {
		t.Fatalf("expected version header, got %#v", event.RequestHeaders)
	}
	if cookie := event.ResponseHeaders["set-cookie"]; cookie != "a=1, b=2" {
		t.Fatalf("expected combined set-cookie header, got %q", cookie)
	}
	if body, ok := event.RequestBody.(map[string]any); !ok || body["id"].(float64) != 123 {
		t.Fatalf("expected parsed request body, got %#v", event.RequestBody)
	}
}

func TestFastHTTPMiddlewareSetsClientIPHeader(t *testing.T) {
	server, err := testserver.StartMockServer(fastHTTPSecretKey, fastHTTPProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor := newFastHTTPMonitor(t, server.Endpoint())
	defer shutdownFastHTTPMonitor(t, monitor)

	handler := aiko.FastHTTPMiddleware(monitor, func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusOK)
	})

	ctx := prepareRequestCtx(fasthttp.MethodGet, "/", nil)
	ctx.Request.Header.Set("X-Forwarded-For", "198.51.100.50, 203.0.113.8")

	handler(ctx)

	if _, err := server.WaitForEvent(3 * time.Second); err != nil {
		t.Fatalf("wait for event: %v", err)
	}

	headers := server.LastRequestHeaders()
	if got := headers.Get("X-Client-IP"); got != "198.51.100.50" {
		t.Fatalf("expected X-Client-IP header 198.51.100.50, got %q", got)
	}
}

func TestFastHTTPMiddlewareCapturesPanics(t *testing.T) {
	server, err := testserver.StartMockServer(fastHTTPSecretKey, fastHTTPProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor := newFastHTTPMonitor(t, server.Endpoint())
	defer shutdownFastHTTPMonitor(t, monitor)

	handler := aiko.FastHTTPMiddleware(monitor, func(ctx *fasthttp.RequestCtx) {
		panic(errors.New("kaboom"))
	})

	ctx := prepareRequestCtx(fasthttp.MethodGet, "/panic", nil)

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("expected panic to propagate")
			}
		}()
		handler(ctx)
	}()

	event, err := server.WaitForEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}
	if event.StatusCode != fasthttp.StatusInternalServerError {
		t.Fatalf("expected 500 status, got %d", event.StatusCode)
	}
	body, ok := event.ResponseBody.(map[string]any)
	if !ok || body["error"] != "kaboom" {
		t.Fatalf("expected panic message, got %#v", event.ResponseBody)
	}
}

/******** helpers/units from internal_test.go that exercise main.go ********/

func TestResponseCaptureEnsureStatusBehavior(t *testing.T) {
	writer := newHijackableResponseWriter(t)
	defer writer.Close()

	capture := aiko.NewResponseCapture(writer)
	if capture.StatusCode() != http.StatusOK {
		t.Fatalf("expected default status OK, got %d", capture.StatusCode())
	}

	capture.EnsureStatus(http.StatusInternalServerError)
	if capture.StatusCode() != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", capture.StatusCode())
	}

	capture.WriteHeader(http.StatusBadRequest)
	capture.EnsureStatus(http.StatusOK)
	if capture.StatusCode() != http.StatusBadRequest {
		t.Fatalf("expected status to remain 400 when ensuring lower code, got %d", capture.StatusCode())
	}

	capture.EnsureStatus(http.StatusInternalServerError)
	if capture.StatusCode() != http.StatusInternalServerError {
		t.Fatalf("expected status to upgrade to 500, got %d", capture.StatusCode())
	}
}

func TestResponseCaptureOptionalInterfaces(t *testing.T) {
	writer := newHijackableResponseWriter(t)
	defer writer.Close()

	capture := aiko.NewResponseCapture(writer)

	capture.Flush()
	if !writer.flushed {
		t.Fatal("expected Flush to call underlying writer")
	}

	conn, rw, err := capture.Hijack()
	if err != nil {
		t.Fatalf("hijack: %v", err)
	}
	if !writer.hijacked {
		t.Fatal("expected underlying hijacker to run")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close conn: %v", err)
	}
	if rw == nil {
		t.Fatal("expected non-nil readwriter")
	}

	if err := capture.Push("/asset.js", nil); err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(writer.pushed) != 1 || writer.pushed[0] != "/asset.js" {
		t.Fatalf("expected push target recorded, got %#v", writer.pushed)
	}
}

func TestCanonicalFastHTTPHeaders(t *testing.T) {
	seq := iter.Seq2[[]byte, []byte](func(yield func([]byte, []byte) bool) {
		if !yield([]byte("Content-Type"), []byte("application/json")) {
			return
		}
		if !yield([]byte("Set-Cookie"), []byte("a=1")) {
			return
		}
		_ = yield([]byte("Set-Cookie"), []byte("b=2"))
	})

	headers := aiko.CanonicalFastHTTPHeaders(seq)
	if headers["content-type"] != "application/json" {
		t.Fatalf("expected content-type header, got %q", headers["content-type"])
	}
	if headers["set-cookie"] != "a=1, b=2" {
		t.Fatalf("expected merged set-cookie, got %q", headers["set-cookie"])
	}
}

func TestStringifyVariants(t *testing.T) {
	if aiko.Stringify("hello") != "hello" {
		t.Fatal("expected string passthrough")
	}
	err := errors.New("boom")
	if aiko.Stringify(err) != "boom" {
		t.Fatal("expected error message passthrough")
	}
	type custom struct{ value int }
	if aiko.Stringify(custom{value: 3}) != "{3}" {
		t.Fatal("expected fmt.Sprint formatting")
	}
}

func TestNewRejectsInvalidSecretEncoding(t *testing.T) {
	_, err := aiko.New(aiko.Config{
		ProjectKey: "pk_AAAAAAAAAAAAAAAAAAAAAA",
		SecretKey:  strings.Repeat("!", 43),
	})
	if err == nil {
		t.Fatal("expected error for invalid secret encoding")
	}
	if !strings.Contains(err.Error(), "decode secret key") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestNewNoopMonitorDisabled(t *testing.T) {
	monitor := aiko.NewNoop()
	if monitor.Enabled() {
		t.Fatal("expected noop monitor to be disabled")
	}
	monitor.AddEvent(aiko.Event{URL: "/noop"})
	if err := monitor.Close(); err != nil {
		t.Fatalf("close noop monitor: %v", err)
	}
	if err := monitor.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown noop monitor: %v", err)
	}
}
