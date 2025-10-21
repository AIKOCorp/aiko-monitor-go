package nethttp_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
	"github.com/aikocorp/aiko-monitor-go/nethttp"
)

func ExampleMiddleware() {
	disabled := false
	monitor, err := aiko.New(aiko.Config{Enabled: &disabled})
	if err != nil {
		panic(err)
	}

	handler := nethttp.Middleware(monitor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	// Output:
	// 200
}
