package aiko_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
)

func ExampleNetHTTPMiddleware() {
	disabled := false
	monitor, err := aiko.New(aiko.Config{Enabled: &disabled})
	if err != nil {
		panic(err)
	}

	handler := aiko.NetHTTPMiddleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	// Output:
	// 200
}
