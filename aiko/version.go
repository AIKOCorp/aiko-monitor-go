package aiko

import "fmt"

const sdkLanguage = "go"

var sdkVersion = "dev"

func SDKVersion() string {
	return sdkVersion
}

func VersionHeaderValue() string {
	return fmt.Sprintf("%s:%s", sdkLanguage, sdkVersion)
}
