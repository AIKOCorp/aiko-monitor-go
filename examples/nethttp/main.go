package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
	"github.com/aikocorp/aiko-monitor-go/nethttp"
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

	mux := http.NewServeMux()

	mux.HandleFunc("/", home)
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/auth/login", login)
	mux.HandleFunc("/auth/register", register)
	mux.HandleFunc("/auth/verify/", verifyToken)
	mux.HandleFunc("/auth/reset-password", resetPassword)
	mux.HandleFunc("/users/", users)
	mux.HandleFunc("/products/", products)
	mux.HandleFunc("/search", search)
	mux.HandleFunc("/filters", filters)
	mux.HandleFunc("/products", getProducts)
	mux.HandleFunc("/newsletter", subscribeNewsletter)
	mux.HandleFunc("/dashboard", getDashboard)
	mux.HandleFunc("/cart/add", addToCart)
	mux.HandleFunc("/profile", getProfile)
	mux.HandleFunc("/error", errorRoute)

	wrapped := nethttp.Middleware(monitor)(mux)

	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", wrapped))
}

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func home(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "<html><body><h1>FastAPI E-commerce API</h1><p>Welcome to our store!</p></body></html>")
}

func health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status":  "healthy",
		"version": "1.0.0",
		"secret":  "hi",
	})
}

func login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Username == "user" && body.Password == "pass" {
		writeJSON(w, 200, map[string]any{
			"token":     "jwt_token_123",
			"user_id":   "user123",
			"ipv4":      "203.0.113.10",
			"ipv6":      "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			"timestamp": time.Now().Unix(),
		})
	} else {
		writeJSON(w, 401, map[string]any{"error": "Invalid credentials"})
	}
}

func register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	writeJSON(w, 200, map[string]any{
		"message": fmt.Sprintf("User %s registered successfully", body.Username),
		"user_id": "user456",
	})
}

func verifyToken(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/auth/verify/")
	var body string
	if token == "abc123" {
		body = "<html><body><h1>Email Verified</h1><p>Your email has been verified successfully!</p></body></html>"
	} else {
		body = "<html><body><h1>Invalid Token</h1><p>Token not found or expired</p></body></html>"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, body)
}

func resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Token == "abc123" {
		writeJSON(w, 200, map[string]string{"message": "Password reset successfully"})
	} else {
		writeJSON(w, 400, map[string]string{"error": "Invalid token"})
	}
}

func users(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/users/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPatch {
		var body struct{ Name string }
		json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, 200, map[string]any{"id": id, "name": body.Name, "updated": true})
	} else if r.Method == http.MethodDelete {
		writeJSON(w, 200, map[string]string{"message": fmt.Sprintf("User %s deleted successfully", id)})
	}
}

func products(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/products/")
	if id == "" || r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Name        string  `json:"name"`
		Price       float64 `json:"price"`
		Description string  `json:"description"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	writeJSON(w, 200, map[string]any{
		"id":          id,
		"name":        body.Name,
		"price":       body.Price,
		"description": body.Description,
		"updated":     true,
	})
}

func search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	writeJSON(w, 200, map[string]any{
		"query": q,
		"results": []any{
			map[string]any{"id": "1", "name": "Laptop", "price": 999.99},
			map[string]any{"id": "2", "name": "Mouse", "price": 29.99},
		},
		"total": 2,
	})
}

func filters(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	writeJSON(w, 200, map[string]any{
		"filters": map[string]any{
			"categories":  []string{"Electronics", "Accessories"},
			"price_range": map[string]float64{"min": 0, "max": 1000},
			"brands":      []string{"Apple", "Dell", "Logitech"},
		},
		"query": q,
	})
}

func getProducts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"products": []any{
			map[string]any{"id": "1", "name": "Laptop", "price": 999.99},
			map[string]any{"id": "2", "name": "Mouse", "price": 29.99},
		},
	})
}

func subscribeNewsletter(w http.ResponseWriter, r *http.Request) {
	var body struct{ Email string }
	json.NewDecoder(r.Body).Decode(&body)
	email := body.Email
	if email == "" {
		email = "unknown"
	}
	writeJSON(w, 200, map[string]string{"message": fmt.Sprintf("Subscribed %s to newsletter", email)})
}

func getDashboard(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"user":        "john_doe",
		"orders":      5,
		"total_spent": 1299.95,
		"last_login":  "2024-01-15",
	})
}

func addToCart(w http.ResponseWriter, r *http.Request) {
	var item struct {
		ProductID string  `json:"product_id"`
		Quantity  uint32  `json:"quantity"`
		Price     float64 `json:"price"`
	}
	json.NewDecoder(r.Body).Decode(&item)
	writeJSON(w, 200, map[string]any{
		"message":    "Item added to cart",
		"item":       item,
		"cart_total": 3,
	})
}

func getProfile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"id":     "123",
		"name":   "John Doe",
		"email":  "john@example.com",
		"orders": 5,
	})
}

func errorRoute(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "something went wrong", 500)
}
