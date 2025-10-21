package aiko

import "fmt"

const sdkLanguage = "go"

// sdkVersion can be overridden via ldflags at build time.
var sdkVersion = "dev"

// SDKVersion returns the current SDK version label.
func SDKVersion() string {
	return sdkVersion
}

// VersionHeaderValue returns the canonical SDK version header payload.
func VersionHeaderValue() string {
	return fmt.Sprintf("%s:%s", sdkLanguage, sdkVersion)
}
