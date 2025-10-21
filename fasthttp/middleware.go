// Package fasthttp provides middleware for instrumenting github.com/valyala/fasthttp handlers.
package fasthttp

import (
	"fmt"
	"strings"
	"time"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
	"github.com/valyala/fasthttp"
)

// Middleware wraps a fasthttp handler with Aiko monitoring.
func Middleware(monitor *aiko.Monitor, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	if monitor == nil || !monitor.Enabled() {
		return next
	}

	return func(ctx *fasthttp.RequestCtx) {
		start := time.Now()

		reqHeaders := canonicalHeaders(ctx.Request.Header.VisitAll)
		reqHeaders["x-aiko-version"] = aiko.VersionHeaderValue()

		reqBody := append([]byte(nil), ctx.PostBody()...)
		requestBody := aiko.ParseJSONBody(reqBody)

		var recovered interface{}

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
		resHeaders := canonicalHeaders(ctx.Response.Header.VisitAll)
		rawRes := append([]byte(nil), ctx.Response.Body()...)

		var responseBody interface{}
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
			responseBody = aiko.DecodeResponseBody(rawRes, resHeaders)
		}

		url := string(ctx.URI().RequestURI())
		endpoint := aiko.EndpointFromURL(url)

		evt := aiko.Event{
			URL:             url,
			Endpoint:        endpoint,
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

func canonicalHeaders(visit func(func(key, value []byte))) map[string]string {
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

func stringify(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case error:
		return val.Error()
	default:
		return fmt.Sprint(val)
	}
}
