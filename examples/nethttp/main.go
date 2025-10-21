package main

import (
	"log"
	"net/http"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
	"github.com/aikocorp/aiko-monitor-go/nethttp"
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

	myHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello, World!"))
	})

	wrappedHandler := nethttp.Middleware(monitor)(myHandler)

	if err := http.ListenAndServe(":8080", wrappedHandler); err != nil {
		log.Fatal(err)
	}
}
