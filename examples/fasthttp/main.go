package main

import (
	"log"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
	aikofasthttp "github.com/aikocorp/aiko-monitor-go/fasthttp"
	"github.com/valyala/fasthttp"
)

func main() {
	monitor, err := aiko.New(aiko.Config{
		ProjectKey: "pk_...",
		SecretKey:  "...",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer monitor.Close()

	handler := func(ctx *fasthttp.RequestCtx) {
		ctx.WriteString("Hello, World!")
	}

	monitored := aikofasthttp.Middleware(monitor, handler)

	if err := fasthttp.ListenAndServe(":8080", monitored); err != nil {
		log.Fatal(err)
	}
}
