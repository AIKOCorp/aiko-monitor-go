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
		Verbose:    true,
		Actor: aiko.ActorConfig{
			Provider: aiko.ActorProviderJWT,
			Token: aiko.ActorTokenConfig{
				Header: &aiko.ActorHeaderTokenConfig{
					Name:    "Authorization",
					Extract: aiko.ActorTokenExtractBearer(),
				},
			},
			Claims: aiko.ActorClaimsConfig{
				ID:    "sub",
				Email: "email",
			},
		},
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
		Verbose:    true,
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

## Verbose install verification

Pass `Verbose: true` in `aiko.Config` while installing the SDK. The SDK keeps ingest behavior unchanged and prints useful details for normal captured requests, including whether monitor accepts the first event.

Example output:

```text
[aiko] verbose init sdk=go:0.0.6 endpoint=https://monitor.aikocorp.ai/api/ingest project_key=pk_AAA...AAAA queue_size=5000 max_concurrent_sends=5
[aiko] verbose captured event_id=evt_... method=GET endpoint=/hello status=200 duration_ms=4
[aiko] verbose queued event_id=evt_... queue_depth=1 queue_size=5000
[aiko] verbose send attempt event_id=evt_... attempt=1 max_attempts=3 method=GET endpoint=/hello payload_bytes=382
[aiko] verbose send accepted event_id=evt_... status=202 request_id=req_... latency_ms=91
[aiko] verbose install verified: monitor accepted first event
```

## Actor extraction

Actor extraction is opt-in. Configure where the auth token lives and which JWT claims map to actor fields. The SDK decodes JWT payloads locally and sends only `actor.provider`, `actor.id`, `actor.email`, and `actor.org_id`. It does not send the token.

For bearer tokens in the `Authorization` header:

```go
monitor, err := aiko.New(aiko.Config{
	ProjectKey: projectKey,
	SecretKey:  secretKey,
	Actor: aiko.ActorConfig{
		Provider: aiko.ActorProviderJWT,
		Token: aiko.ActorTokenConfig{
			Header: &aiko.ActorHeaderTokenConfig{
				Name:    "Authorization",
				Extract: aiko.ActorTokenExtractBearer(),
			},
		},
		Claims: aiko.ActorClaimsConfig{
			ID:    "sub",
			Email: "email",
		},
	},
})
```

For JSON cookies that contain the JWT under a field such as `access_token`:

```go
monitor, err := aiko.New(aiko.Config{
	ProjectKey: projectKey,
	SecretKey:  secretKey,
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
```

For Supabase auth cookies, configure the cookie name and the claims explicitly. The cookie name is usually `sb-<project-ref>-auth-token`.

```go
monitor, err := aiko.New(aiko.Config{
	ProjectKey: projectKey,
	SecretKey:  secretKey,
	Actor: aiko.ActorConfig{
		Provider: aiko.ActorProviderSupabase,
		Token: aiko.ActorTokenConfig{
			Cookie: &aiko.ActorCookieTokenConfig{
				Name: "sb-<project-ref>-auth-token",
			},
		},
		Claims: aiko.ActorClaimsConfig{
			ID:    "sub",
			Email: "email",
		},
	},
})
```

For opaque or encrypted sessions, use a custom resolver and return the actor fields yourself:

```go
monitor, err := aiko.New(aiko.Config{
	ProjectKey: projectKey,
	SecretKey:  secretKey,
	Actor: aiko.ActorConfig{
		Provider: aiko.ActorProviderCustom,
		Resolve: func(ctx aiko.ActorResolveContext) (*aiko.ActorContext, error) {
			user := userFromRequest(ctx.HTTPRequest)
			if user == nil {
				return nil, nil
			}
			return &aiko.ActorContext{
				Provider: aiko.ActorProviderCustom,
				ID:       user.ID,
				Email:    user.Email,
				OrgID:    user.OrgID,
			}, nil
		},
	},
})
```

For nested claims, use dot paths such as `user.id` or `claims.email`.

Expected JWT payload shape:

```json
{
  "exp": 1781326327,
  "sub": "8e9ccf29-7838-46e3-bafc-b0a91f14b20a",
  "email": "pixqc1159@gmail.com"
}
```

## FAQ

**Does this mutate my responses/requests?**
No. Bodies are buffered for capture; headers are read and normalized in memory. The extra `x-aiko-version` is only added to the recorded metadata, not sent to clients.

**Do you support other programming languages or frameworks?**
Yes. We have JavaScript (Express, NextJS), Python (FastAPI, Flask), Rust (Actix). We are adding more frameworks to AIKO SDKs, don't hesitate to let us know which framework we should support next!
