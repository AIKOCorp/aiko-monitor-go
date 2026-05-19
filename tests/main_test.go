package aiko_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestNetHTTPMiddlewareExtractsJWTActor(t *testing.T) {
	server, err := testserver.StartMockServer(middlewareSecretKey, middlewareProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Endpoint:   server.Endpoint(),
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderJWT,
			Token: aiko.ActorTokenConfig{
				Header: &aiko.ActorHeaderTokenConfig{
					Name:    "X-Auth-Token",
					Extract: aiko.ActorTokenExtractBearer(),
				},
			},
			Claims: aiko.ActorClaimsConfig{
				ID:    "id",
				Email: "sub",
			},
		},
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownMonitorHelper(t, monitor)

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	token := testJWT(t, map[string]any{
		"exp": 1781326327,
		"id":  "8e9ccf29-7838-46e3-bafc-b0a91f14b20a",
		"sub": "pixqc1159@gmail.com",
	})
	req := httptest.NewRequest(http.MethodGet, "http://example.com/private", nil)
	req.Header.Set("X-Auth-Token", "Bearer "+token)

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	event, err := server.WaitForEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}
	if event.Actor == nil {
		t.Fatal("expected actor context")
	}
	if event.Actor.Provider != aiko.ActorProviderJWT {
		t.Fatalf("expected actor provider jwt, got %q", event.Actor.Provider)
	}
	if event.Actor.ID != "8e9ccf29-7838-46e3-bafc-b0a91f14b20a" {
		t.Fatalf("expected actor id, got %q", event.Actor.ID)
	}
	if event.Actor.Email != "pixqc1159@gmail.com" {
		t.Fatalf("expected actor email, got %q", event.Actor.Email)
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if strings.Contains(string(eventJSON), token) {
		t.Fatal("expected raw token to be omitted from event payload")
	}
	if event.RequestHeaders["x-auth-token"] != "[REDACTED]" {
		t.Fatalf("expected configured auth header redacted, got %#v", event.RequestHeaders)
	}
	for _, field := range []string{"session_id", "org_id", "roles", "source"} {
		if strings.Contains(string(eventJSON), field) {
			t.Fatalf("expected deferred actor field %q to be omitted", field)
		}
	}
}

func TestNetHTTPMiddlewareOmitsActorWhenConfiguredClaimsDoNotResolve(t *testing.T) {
	server, err := testserver.StartMockServer(middlewareSecretKey, middlewareProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Endpoint:   server.Endpoint(),
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderJWT,
			Token: aiko.ActorTokenConfig{
				Header: &aiko.ActorHeaderTokenConfig{
					Name:    "Authorization",
					Extract: aiko.ActorTokenExtractBearer(),
				},
			},
			Claims: aiko.ActorClaimsConfig{
				ID:    "actor.id",
				Email: "actor.email",
			},
		},
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownMonitorHelper(t, monitor)

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/private", nil)
	req.Header.Set("Authorization", "Bearer "+testJWT(t, map[string]any{"sub": "user_1"}))

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	event, err := server.WaitForEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}
	if event.Actor != nil {
		t.Fatalf("expected actor to be omitted, got %#v", event.Actor)
	}
}

func TestNetHTTPMiddlewareExtractsJWTActorFromJSONCookie(t *testing.T) {
	server, err := testserver.StartMockServer(middlewareSecretKey, middlewareProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Endpoint:   server.Endpoint(),
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderJWT,
			Token: aiko.ActorTokenConfig{
				Cookie: &aiko.ActorCookieTokenConfig{
					Name:    "aiko_auth_token",
					Extract: aiko.ActorTokenExtractJSON("access_token"),
				},
			},
			Claims: aiko.ActorClaimsConfig{
				ID:    "uid",
				Email: "sub",
				OrgID: "org_id",
			},
		},
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownMonitorHelper(t, monitor)

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	token := testJWT(t, map[string]any{"uid": "usr_alex", "sub": "alex@example.com", "org_id": "org_1"})
	cookieValue := urlEscapedJSON(t, map[string]any{"access_token": token, "token_type": "bearer"})
	req := httptest.NewRequest(http.MethodGet, "http://example.com/private", nil)
	req.Header.Set("Cookie", "aiko_auth_token="+cookieValue)

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	event, err := server.WaitForEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}
	if event.Actor == nil {
		t.Fatal("expected actor context")
	}
	if event.Actor.Provider != aiko.ActorProviderJWT || event.Actor.ID != "usr_alex" || event.Actor.Email != "alex@example.com" || event.Actor.OrgID != "org_1" {
		t.Fatalf("unexpected actor: %#v", event.Actor)
	}
	if event.RequestHeaders["cookie"] != "[REDACTED]" {
		t.Fatalf("expected cookie redacted, got %#v", event.RequestHeaders)
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if strings.Contains(string(eventJSON), token) {
		t.Fatal("expected raw token to be omitted from event payload")
	}
}

func TestNetHTTPMiddlewareExtractsSupabaseActorFromCookie(t *testing.T) {
	server, err := testserver.StartMockServer(middlewareSecretKey, middlewareProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Endpoint:   server.Endpoint(),
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderSupabase,
			Token: aiko.ActorTokenConfig{
				Cookie: &aiko.ActorCookieTokenConfig{Name: "sb-project-auth-token"},
			},
			Claims: aiko.ActorClaimsConfig{
				ID:    "sub",
				Email: "email",
			},
		},
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownMonitorHelper(t, monitor)

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	token := testJWT(t, map[string]any{"sub": "usr_alex", "email": "alex@example.com"})
	cookieValue := urlEscapedJSON(t, map[string]any{"access_token": token, "token_type": "bearer"})
	req := httptest.NewRequest(http.MethodGet, "http://example.com/private", nil)
	req.Header.Set("Cookie", "sb-project-auth-token="+cookieValue)

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	event, err := server.WaitForEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}
	if event.Actor == nil {
		t.Fatal("expected actor context")
	}
	if event.Actor.Provider != aiko.ActorProviderSupabase || event.Actor.ID != "usr_alex" || event.Actor.Email != "alex@example.com" {
		t.Fatalf("unexpected actor: %#v", event.Actor)
	}
	if event.RequestHeaders["cookie"] != "[REDACTED]" {
		t.Fatalf("expected cookie redacted, got %#v", event.RequestHeaders)
	}
}

func TestNetHTTPMiddlewareLogsActorDiagnosticsWhenVerbose(t *testing.T) {
	server, err := testserver.StartMockServer(middlewareSecretKey, middlewareProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	var logs bytes.Buffer
	monitor, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Endpoint:   server.Endpoint(),
		Verbose:    true,
		Logger:     log.New(&logs, "", 0),
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderJWT,
			Token: aiko.ActorTokenConfig{
				Header: &aiko.ActorHeaderTokenConfig{
					Name:    "Authorization",
					Extract: aiko.ActorTokenExtractBearer(),
				},
			},
			Claims: aiko.ActorClaimsConfig{ID: "sub", Email: "email"},
		},
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownMonitorHelper(t, monitor)

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	token := testJWT(t, map[string]any{"sub": "usr_alex", "email": "alex@example.com"})
	req := httptest.NewRequest(http.MethodGet, "http://example.com/private", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if _, err := server.WaitForEvent(3 * time.Second); err != nil {
		t.Fatalf("wait for event: %v", err)
	}
	output := logs.String()
	for _, expected := range []string{
		"actor configured provider=jwt",
		"carrier=header",
		"extractor=bearer",
		"claim_id=sub",
		"actor resolved provider=jwt id=yes email=yes org_id=no",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected verbose output to contain %q, got %q", expected, output)
		}
	}
	if strings.Contains(output, token) {
		t.Fatal("expected raw token to be omitted from verbose logs")
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

func testJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal jwt header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt claims: %v", err)
	}
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		signature
}

func urlEscapedJSON(t *testing.T, value map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json cookie: %v", err)
	}
	return url.QueryEscape(string(raw))
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
	if cookie := event.ResponseHeaders["set-cookie"]; cookie != "[REDACTED]" {
		t.Fatalf("expected set-cookie redacted, got %q", cookie)
	}
	if body, ok := event.RequestBody.(map[string]any); !ok || body["id"].(float64) != 123 {
		t.Fatalf("expected parsed request body, got %#v", event.RequestBody)
	}
}

func TestFastHTTPMiddlewareExtractsJWTActor(t *testing.T) {
	server, err := testserver.StartMockServer(fastHTTPSecretKey, fastHTTPProjectKey)
	if err != nil {
		t.Fatalf("start mock server: %v", err)
	}
	defer server.Stop()

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: fastHTTPProjectKey,
		SecretKey:  fastHTTPSecretKey,
		Endpoint:   server.Endpoint(),
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderJWT,
			Token: aiko.ActorTokenConfig{
				Header: &aiko.ActorHeaderTokenConfig{
					Name:    "Authorization",
					Extract: aiko.ActorTokenExtractBearer(),
				},
			},
			Claims: aiko.ActorClaimsConfig{
				ID:    "actor.id",
				Email: "actor.email",
			},
		},
	})
	if err != nil {
		t.Fatalf("init monitor: %v", err)
	}
	defer shutdownFastHTTPMonitor(t, monitor)

	handler := aiko.FastHTTPMiddleware(monitor, func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusNoContent)
	})

	token := testJWT(t, map[string]any{
		"exp": 1781326327,
		"actor": map[string]any{
			"id":    "8e9ccf29-7838-46e3-bafc-b0a91f14b20a",
			"email": "pixqc1159@gmail.com",
		},
	})
	ctx := prepareRequestCtx(fasthttp.MethodGet, "/private", nil)
	ctx.Request.Header.Set("Authorization", "Bearer "+token)

	handler(ctx)

	event, err := server.WaitForEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("wait for event: %v", err)
	}
	if event.Actor == nil {
		t.Fatal("expected actor context")
	}
	if event.Actor.Provider != aiko.ActorProviderJWT {
		t.Fatalf("expected actor provider jwt, got %q", event.Actor.Provider)
	}
	if event.Actor.ID != "8e9ccf29-7838-46e3-bafc-b0a91f14b20a" {
		t.Fatalf("expected actor id, got %q", event.Actor.ID)
	}
	if event.Actor.Email != "pixqc1159@gmail.com" {
		t.Fatalf("expected actor email, got %q", event.Actor.Email)
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

func TestNewRejectsActorFieldsWithoutProvider(t *testing.T) {
	_, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Actor: aiko.ActorConfig{
			Claims: aiko.ActorClaimsConfig{ID: "id", Email: "email"},
		},
	})
	if err == nil {
		t.Fatal("expected actor provider config error")
	}
	expected := "actor.provider is required when actor fields are configured"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}

func TestNewRejectsActorProviderWithoutToken(t *testing.T) {
	_, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderJWT,
			Claims:   aiko.ActorClaimsConfig{ID: "sub", Email: "email"},
		},
	})
	if err == nil {
		t.Fatal("expected actor token config error")
	}
	expected := "actor.token is required when actor.provider is configured"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}

func TestNewRejectsJWTActorWithoutExtract(t *testing.T) {
	_, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderJWT,
			Token: aiko.ActorTokenConfig{
				Header: &aiko.ActorHeaderTokenConfig{Name: "Authorization"},
			},
			Claims: aiko.ActorClaimsConfig{ID: "sub", Email: "email"},
		},
	})
	if err == nil {
		t.Fatal("expected actor extract config error")
	}
	expected := "actor token extract is required when actor.provider is jwt"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}

func TestNewRejectsSupabaseActorWithExtract(t *testing.T) {
	_, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderSupabase,
			Token: aiko.ActorTokenConfig{
				Cookie: &aiko.ActorCookieTokenConfig{
					Name:    "sb-project-auth-token",
					Extract: aiko.ActorTokenExtractRaw(),
				},
			},
			Claims: aiko.ActorClaimsConfig{ID: "sub", Email: "email"},
		},
	})
	if err == nil {
		t.Fatal("expected supabase extract config error")
	}
	expected := "actor.token.cookie.extract is not allowed when actor.provider is supabase"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
	}
}

func TestNewRejectsUnsupportedActorProvider(t *testing.T) {
	_, err := aiko.New(aiko.Config{
		ProjectKey: middlewareProjectKey,
		SecretKey:  middlewareSecretKey,
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProvider("clerk"),
			Token: aiko.ActorTokenConfig{
				Header: &aiko.ActorHeaderTokenConfig{
					Name:    "Authorization",
					Extract: aiko.ActorTokenExtractBearer(),
				},
			},
			Claims: aiko.ActorClaimsConfig{ID: "id", Email: "email"},
		},
	})
	if err == nil {
		t.Fatal("expected actor provider config error")
	}
	expected := "actor.provider must be jwt, supabase, or custom"
	if err.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, err.Error())
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
