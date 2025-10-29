package aiko_test

import (
	"fmt"

	"github.com/valyala/fasthttp"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
)

func ExampleFastHTTPMiddleware() {
	disabled := false
	monitor, err := aiko.New(aiko.Config{Enabled: &disabled})
	if err != nil {
		panic(err)
	}

	handler := aiko.FastHTTPMiddleware(monitor, func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusOK)
		_, _ = ctx.Write([]byte("ok"))
	})

	req := fasthttp.AcquireRequest()
	req.Header.SetMethod(fasthttp.MethodGet)
	req.SetRequestURI("/")

	ctx := &fasthttp.RequestCtx{}
	ctx.Init(req, nil, nil)

	handler(ctx)

	fmt.Println(ctx.Response.StatusCode())
	// Output:
	// 200
}
