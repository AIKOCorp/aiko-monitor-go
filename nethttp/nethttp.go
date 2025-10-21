// Package nethttp provides middleware for instrumenting net/http handlers.
package nethttp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
)

// Middleware returns a net/http middleware that records requests via the provided monitor.
func Middleware(monitor *aiko.Monitor) func(http.Handler) http.Handler {
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

			reqHeaders := aiko.CanonicalHeaders(r.Header)
			reqHeaders["x-aiko-version"] = aiko.VersionHeaderValue()
			requestBody := aiko.ParseJSONBody(reqBodyBuf)

			capture := newResponseCapture(w)
			var recovered interface{}

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
			resHeaders := aiko.CanonicalHeaders(capture.Header())
			rawRes := capture.body.Bytes()
			statusCode := capture.statusCode()

			var responseBody interface{}
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
				responseBody = aiko.DecodeResponseBody(rawRes, resHeaders)
			}

			requestURI := r.URL.RequestURI()
			endpoint := aiko.EndpointFromURL(requestURI)

			evt := aiko.Event{
				URL:             requestURI,
				Endpoint:        endpoint,
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
