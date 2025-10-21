package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
	aikofasthttp "github.com/aikocorp/aiko-monitor-go/fasthttp"
	"github.com/valyala/fasthttp"
)

func main() {
	monitor, err := aiko.New(aiko.Config{
		ProjectKey: "pk_xNIiFZwJ8tu1GLNsCs4P4w",
		SecretKey:  "p_E1ygBt4NQgBpN4pCkuklWIYCpxPNJ5ALU4ooULfdw",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer monitor.Close()

	handler := func(ctx *fasthttp.RequestCtx) {
		path := string(ctx.Path())
		method := string(ctx.Method())

		switch {
		case path == "/" && method == fasthttp.MethodGet:
			ctx.SetContentType("text/html; charset=utf-8")
			fmt.Fprint(ctx, `<html><body><h1>FastAPI E-commerce API</h1><p>Welcome to our store!</p></body></html>`)

		case path == "/health" && method == fasthttp.MethodGet:
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"status":  "healthy",
				"version": "1.0.0",
				"secret":  "hi",
			})

		case path == "/auth/login" && method == fasthttp.MethodPost:
			var body struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			json.Unmarshal(ctx.PostBody(), &body)
			if body.Username == "user" && body.Password == "pass" {
				writeJSON(ctx, fasthttp.StatusOK, map[string]any{
					"token":     "jwt_token_123",
					"user_id":   "user123",
					"ipv4":      "203.0.113.10",
					"ipv6":      "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
					"timestamp": time.Now().Unix(),
				})
			} else {
				writeJSON(ctx, fasthttp.StatusUnauthorized, map[string]string{"error": "Invalid credentials"})
			}

		case path == "/auth/register" && method == fasthttp.MethodPost:
			var body struct {
				Username string `json:"username"`
				Email    string `json:"email"`
				Password string `json:"password"`
			}
			json.Unmarshal(ctx.PostBody(), &body)
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"message": fmt.Sprintf("User %s registered successfully", body.Username),
				"user_id": "user456",
			})

		case strings.HasPrefix(path, "/auth/verify/") && method == fasthttp.MethodGet:
			token := strings.TrimPrefix(path, "/auth/verify/")
			ctx.SetContentType("text/html; charset=utf-8")
			if token == "abc123" {
				fmt.Fprint(ctx, `<html><body><h1>Email Verified</h1><p>Your email has been verified successfully!</p></body></html>`)
			} else {
				fmt.Fprint(ctx, `<html><body><h1>Invalid Token</h1><p>Token not found or expired</p></body></html>`)
			}

		case path == "/auth/reset-password" && method == fasthttp.MethodPost:
			var body struct {
				Token    string `json:"token"`
				Password string `json:"password"`
			}
			json.Unmarshal(ctx.PostBody(), &body)
			if body.Token == "abc123" {
				writeJSON(ctx, fasthttp.StatusOK, map[string]string{"message": "Password reset successfully"})
			} else {
				writeJSON(ctx, fasthttp.StatusBadRequest, map[string]string{"error": "Invalid token"})
			}

		case strings.HasPrefix(path, "/users/") && method == fasthttp.MethodPatch:
			id := strings.TrimPrefix(path, "/users/")
			var body struct {
				Name string `json:"name"`
			}
			json.Unmarshal(ctx.PostBody(), &body)
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"id":      id,
				"name":    body.Name,
				"updated": true,
			})

		case strings.HasPrefix(path, "/users/") && method == fasthttp.MethodDelete:
			id := strings.TrimPrefix(path, "/users/")
			writeJSON(ctx, fasthttp.StatusOK, map[string]string{
				"message": fmt.Sprintf("User %s deleted successfully", id),
			})

		case strings.HasPrefix(path, "/products/") && method == fasthttp.MethodPut:
			id := strings.TrimPrefix(path, "/products/")
			var body struct {
				Name        string  `json:"name"`
				Price       float64 `json:"price"`
				Description string  `json:"description"`
			}
			json.Unmarshal(ctx.PostBody(), &body)
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"id":          id,
				"name":        body.Name,
				"price":       body.Price,
				"description": body.Description,
				"updated":     true,
			})

		case path == "/search" && method == fasthttp.MethodGet:
			q := string(ctx.QueryArgs().Peek("q"))
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"query": q,
				"results": []any{
					map[string]any{"id": "1", "name": "Laptop", "price": 999.99},
					map[string]any{"id": "2", "name": "Mouse", "price": 29.99},
				},
				"total": 2,
			})

		case path == "/filters" && method == fasthttp.MethodGet:
			q := string(ctx.QueryArgs().Peek("q"))
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"filters": map[string]any{
					"categories":  []string{"Electronics", "Accessories"},
					"price_range": map[string]float64{"min": 0, "max": 1000},
					"brands":      []string{"Apple", "Dell", "Logitech"},
				},
				"query": q,
			})

		case path == "/products" && method == fasthttp.MethodGet:
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"products": []any{
					map[string]any{"id": "1", "name": "Laptop", "price": 999.99},
					map[string]any{"id": "2", "name": "Mouse", "price": 29.99},
				},
			})

		case path == "/newsletter" && method == fasthttp.MethodPost:
			var body struct {
				Email string `json:"email"`
			}
			json.Unmarshal(ctx.PostBody(), &body)
			email := body.Email
			if email == "" {
				email = "unknown"
			}
			writeJSON(ctx, fasthttp.StatusOK, map[string]string{
				"message": fmt.Sprintf("Subscribed %s to newsletter", email),
			})

		case path == "/dashboard" && method == fasthttp.MethodGet:
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"user":        "john_doe",
				"orders":      5,
				"total_spent": 1299.95,
				"last_login":  "2024-01-15",
			})

		case path == "/cart/add" && method == fasthttp.MethodPost:
			var item struct {
				ProductID string  `json:"product_id"`
				Quantity  uint32  `json:"quantity"`
				Price     float64 `json:"price"`
			}
			json.Unmarshal(ctx.PostBody(), &item)
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"message":    "Item added to cart",
				"item":       item,
				"cart_total": 3,
			})

		case path == "/profile" && method == fasthttp.MethodGet:
			writeJSON(ctx, fasthttp.StatusOK, map[string]any{
				"id":     "123",
				"name":   "John Doe",
				"email":  "john@example.com",
				"orders": 5,
			})

		case path == "/error" && method == fasthttp.MethodGet:
			ctx.Error("something went wrong", fasthttp.StatusInternalServerError)

		default:
			ctx.Error("Not Found", fasthttp.StatusNotFound)
		}
	}

	monitored := aikofasthttp.Middleware(monitor, handler)
	log.Println("Listening on :8081")
	log.Fatal(fasthttp.ListenAndServe(":8081", monitored))
}

func writeJSON(ctx *fasthttp.RequestCtx, status int, data any) {
	ctx.SetStatusCode(status)
	ctx.SetContentType("application/json")
	json.NewEncoder(ctx).Encode(data)
}
