package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	aiko "github.com/aikocorp/aiko-monitor-go"
)

func main() {
	projectKey := os.Getenv("AIKO_PROJECT_KEY")
	secretKey := os.Getenv("AIKO_SECRET_KEY")
	if projectKey == "" || secretKey == "" {
		log.Fatal("AIKO_PROJECT_KEY and AIKO_SECRET_KEY must be set in the environment")
	}

	monitor, err := aiko.Init(aiko.Config{
		ProjectKey: projectKey,
		SecretKey:  secretKey,
	})
	if err != nil {
		log.Fatalf("init monitor: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"hello from aiko"}`))
	})

	server := &http.Server{
		Addr:    ":8080",
		Handler: monitor.Middleware(mux),
	}

	go func() {
		log.Println("[example] listening on http://localhost:8080")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen and serve: %v", err)
		}
	}()

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt)
	<-shutdownCh
	log.Println("[example] shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	if err := monitor.Shutdown(ctx); err != nil {
		log.Printf("monitor shutdown: %v", err)
	}

	log.Println("[example] exited cleanly")
}
