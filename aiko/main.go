package aiko

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
)

func New(cfg Config) (*Monitor, error) {
	monitor, err := initMonitor(cfg)
	if err != nil {
		return nil, err
	}
	return monitor, nil
}

func NewNoop() *Monitor {
	return newNoopMonitor(Config{})
}

func NetHTTPMiddleware(monitor *Monitor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if monitor == nil || !monitor.Enabled() {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			var reqBodyBuf []byte
			if r.Body != nil {
				reqBodyBuf, _ = io.ReadAll(r.Body)
				_ = r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(reqBodyBuf))
			}

			reqHeaders := CanonicalHeaders(r.Header)
			reqHeaders["x-aiko-version"] = VersionHeaderValue()
			requestBody := ParseJSONBody(reqBodyBuf)

			capture := newResponseCapture(w)
			var recovered any

			func() {
				defer func() {
					if rec := recover(); rec != nil {
						recovered = rec
						capture.ensureStatus(http.StatusInternalServerError)
					}
				}()
				next.ServeHTTP(capture, r)
			}()

			duration := time.Since(start)
			resHeaders := CanonicalHeaders(capture.Header())
			rawRes := capture.body.Bytes()
			statusCode := capture.statusCode()

			var responseBody any
			switch {
			case recovered != nil:
				responseBody = map[string]string{"error": fmt.Sprint(recovered)}
			case len(rawRes) == 0 && statusCode >= 500:
				text := http.StatusText(statusCode)
				if text == "" {
					text = "Internal Server Error"
				}
				responseBody = map[string]string{"error": text}
			default:
				responseBody = DecodeResponseBody(rawRes, resHeaders)
			}

			requestURI := r.URL.RequestURI()

			evt := Event{
				URL:             requestURI,
				Endpoint:        requestURI,
				Method:          strings.ToUpper(r.Method),
				StatusCode:      statusCode,
				RequestHeaders:  reqHeaders,
				RequestBody:     requestBody,
				ResponseHeaders: resHeaders,
				ResponseBody:    responseBody,
				DurationMS:      duration.Milliseconds(),
			}

			monitor.AddEvent(evt)

			if recovered != nil {
				panic(recovered)
			}
		})
	}
}

func FastHTTPMiddleware(monitor *Monitor, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	if monitor == nil || !monitor.Enabled() {
		return next
	}

	return func(ctx *fasthttp.RequestCtx) {
		start := time.Now()

		reqHeaders := canonicalFastHTTPHeaders(ctx.Request.Header.VisitAll)
		reqHeaders["x-aiko-version"] = VersionHeaderValue()

		reqBody := append([]byte(nil), ctx.PostBody()...)
		requestBody := ParseJSONBody(reqBody)

		var recovered any

		func() {
			defer func() {
				if rec := recover(); rec != nil {
					recovered = rec
					ctx.Response.ResetBody()
					ctx.Response.SetStatusCode(fasthttp.StatusInternalServerError)
				}
			}()
			next(ctx)
		}()

		status := ctx.Response.StatusCode()
		resHeaders := canonicalFastHTTPHeaders(ctx.Response.Header.VisitAll)
		rawRes := append([]byte(nil), ctx.Response.Body()...)

		var responseBody any
		switch {
		case recovered != nil:
			responseBody = map[string]string{"error": stringify(recovered)}
		case len(rawRes) == 0 && status >= 500:
			msg := fasthttp.StatusMessage(status)
			if msg == "" {
				msg = "Internal Server Error"
			}
			responseBody = map[string]string{"error": msg}
		default:
			responseBody = DecodeResponseBody(rawRes, resHeaders)
		}

		url := string(ctx.URI().RequestURI())

		evt := Event{
			URL:             url,
			Endpoint:        url,
			Method:          strings.ToUpper(string(ctx.Method())),
			StatusCode:      status,
			RequestHeaders:  reqHeaders,
			RequestBody:     requestBody,
			ResponseHeaders: resHeaders,
			ResponseBody:    responseBody,
			DurationMS:      time.Since(start).Milliseconds(),
		}

		monitor.AddEvent(evt)

		if recovered != nil {
			panic(recovered)
		}
	}
}

type responseCapture struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func newResponseCapture(w http.ResponseWriter) *responseCapture {
	return &responseCapture{ResponseWriter: w}
}

func (rw *responseCapture) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseCapture) Write(b []byte) (int, error) {
	if len(b) > 0 {
		rw.body.Write(b)
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *responseCapture) ensureStatus(code int) {
	if rw.status == 0 || rw.status < code {
		rw.status = code
	}
}

func (rw *responseCapture) statusCode() int {
	if rw.status == 0 {
		return http.StatusOK
	}
	return rw.status
}

func (rw *responseCapture) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (rw *responseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("response writer does not support hijacking")
}

func (rw *responseCapture) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := rw.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

var (
	_ http.Flusher  = (*responseCapture)(nil)
	_ http.Hijacker = (*responseCapture)(nil)
	_ http.Pusher   = (*responseCapture)(nil)
)

func canonicalFastHTTPHeaders(visit func(func(key, value []byte))) map[string]string {
	headers := make(map[string]string)
	visit(func(k, v []byte) {
		key := strings.ToLower(string(k))
		val := string(v)
		if existing, ok := headers[key]; ok && existing != "" {
			headers[key] = existing + ", " + val
		} else {
			headers[key] = val
		}
	})
	return headers
}

func stringify(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case error:
		return val.Error()
	default:
		return fmt.Sprint(val)
	}
}
