# aiko-monitor-go

Monitor your Golang API with AIKO!

## Install

```bash
go get github.com/aikocorp/aiko-monitor-go/aiko
```

## Quick start (net/http)

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
)

func main() {
	_ = godotenv.Load()

	projectKey := os.Getenv("AIKO_PROJECT_KEY")
	secretKey := os.Getenv("AIKO_SECRET_KEY")
	if projectKey == "" || secretKey == "" {
		log.Fatal("AIKO_PROJECT_KEY and AIKO_SECRET_KEY must be set (e.g. via .env)")
	}

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: projectKey,
		SecretKey:  secretKey,
	})
	if err != nil {
		log.Fatalf("aiko init: %v", err)
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = monitor.Shutdown(ctx)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"message":"ok"}`)
	})

	handler := aiko.NetHTTPMiddleware(monitor)(mux)

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
```

### Quick start (fasthttp)

```go
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/valyala/fasthttp"
	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
)

func main() {
	_ = godotenv.Load()

	projectKey := os.Getenv("AIKO_PROJECT_KEY")
	secretKey := os.Getenv("AIKO_SECRET_KEY")
	if projectKey == "" || secretKey == "" {
		log.Fatal("AIKO_PROJECT_KEY and AIKO_SECRET_KEY must be set (e.g. via .env)")
	}

	monitor, err := aiko.New(aiko.Config{
		ProjectKey: projectKey,
		SecretKey:  secretKey,
	})
	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = monitor.Shutdown(ctx)
	}()

	handler := aiko.FastHTTPMiddleware(monitor, func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Content-Type", "application/json")
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBody([]byte(`{"ok":true}`))
	})

	log.Fatal(fasthttp.ListenAndServe(":8080", handler))
}
```

## FAQ

**Does this mutate my responses/requests?**
No. Bodies are buffered for capture; headers are read and normalized in memory. The extra `x-aiko-version` is only added to the recorded metadata, not sent to clients.

**Do you support other programming languages or frameworks?**
Yes. We have JavaScript (Express, NextJS), Python (FastAPI, Flask), Rust (Actix). We are adding more frameworks to AIKO SDKs, don't hesitate to let us know which framework we should support next!
