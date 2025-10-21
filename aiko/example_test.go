package aiko_test

import (
	"fmt"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
)

func ExampleNew() {
	enabled := false
	monitor, err := aiko.New(aiko.Config{Enabled: &enabled})
	if err != nil {
		panic(err)
	}
	fmt.Println(monitor.Enabled())
	// Output:
	// false
}

func ExampleEndpointFromURL() {
	path := aiko.EndpointFromURL("https://api.service.dev/v1/resources?id=7")
	fmt.Println(path)
	// Output:
	// /v1/resources
}
