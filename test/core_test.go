package aiko_test

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	aiko "github.com/aikocorp/aiko-monitor-go/aiko"
)

func TestCanonicalHeadersFlattensAndLowercases(t *testing.T) {
	headers := http.Header{}
	headers.Add("Content-Type", "application/json")
	headers.Add("Set-Cookie", "a=1")
	headers.Add("Set-Cookie", "b=2")

	canon := aiko.CanonicalHeaders(headers)
	if canon["content-type"] != "application/json" {
		t.Fatalf("expected lowercased content-type, got %q", canon["content-type"])
	}
	if canon["set-cookie"] != "a=1, b=2" {
		t.Fatalf("expected joined set-cookie, got %q", canon["set-cookie"])
	}
}

func TestRedactStringMasksIPAddresses(t *testing.T) {
	input := "contact user@example.com at 2001:0DB8:85A3:0000:0000:8A2E:0370:7334 or 203.0.113.10"
	output := aiko.RedactString(input)
	if output == input {
		t.Fatal("expected redaction to modify string")
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("expected redaction mask in output: %q", output)
	}
	lower := strings.ToLower(output)
	if strings.Contains(lower, "2001:0db8") || strings.Contains(lower, "203.0.113.10") {
		t.Fatalf("expected IP addresses to be masked: %q", output)
	}
}

func TestRedactArrayElements(t *testing.T) {
	value := []any{"user@example.com", "no pii"}
	redacted := aiko.RedactValue(value)

	arr, ok := redacted.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", redacted)
	}
	if arr[0] != "[REDACTED]" {
		t.Fatalf("expected first element redacted, got %v", arr[0])
	}
	if arr[1] != "no pii" {
		t.Fatalf("expected safe element unchanged, got %v", arr[1])
	}
}

func TestDecodeResponseBodyRespectsContentTypes(t *testing.T) {
	jsonBody := []byte(`{"x":1}`)
	jsonHeaders := map[string]string{"content-type": "application/json"}
	parsed := aiko.DecodeResponseBody(jsonBody, jsonHeaders)
	obj, ok := parsed.(map[string]any)
	if !ok || obj["x"].(float64) != 1 {
		t.Fatalf("expected JSON object, got %#v", parsed)
	}

	textBody := []byte("<html></html>")
	textHeaders := map[string]string{"content-type": "text/html"}
	parsed = aiko.DecodeResponseBody(textBody, textHeaders)
	if str, ok := parsed.(string); !ok || str != "<html></html>" {
		t.Fatalf("expected string body, got %#v", parsed)
	}

	binaryBody := []byte{0, 1, 2}
	binaryHeaders := map[string]string{"content-type": "application/octet-stream"}
	parsed = aiko.DecodeResponseBody(binaryBody, binaryHeaders)
	objMap, ok := parsed.(map[string]string)
	if !ok {
		t.Fatalf("expected base64 map, got %#v", parsed)
	}
	if objMap["base64"] != base64.StdEncoding.EncodeToString(binaryBody) {
		t.Fatalf("expected base64 encoding, got %v", objMap["base64"])
	}
}

func TestDecodeResponseBodyBestEffortWithoutHeaders(t *testing.T) {
	jsonBody := []byte(`{"message":"ok"}`)
	parsed := aiko.DecodeResponseBody(jsonBody, map[string]string{})
	obj, ok := parsed.(map[string]any)
	if !ok || obj["message"].(string) != "ok" {
		t.Fatalf("expected JSON object fallback, got %#v", parsed)
	}

	textBody := []byte("plain text")
	parsed = aiko.DecodeResponseBody(textBody, map[string]string{})
	if str, ok := parsed.(string); !ok || str != "plain text" {
		t.Fatalf("expected string fallback, got %#v", parsed)
	}
}

func TestDecodeResponseBodyHandlesInvalidJSON(t *testing.T) {
	body := []byte("not-json")
	headers := map[string]string{"content-type": "application/json"}
	parsed := aiko.DecodeResponseBody(body, headers)
	if str, ok := parsed.(string); !ok || str != "not-json" {
		t.Fatalf("expected invalid JSON to return raw string, got %#v", parsed)
	}
}

func TestEndpointFromURLDerivesPathCorrectly(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"http://example.com/test?foo=1", "/test"},
		{"/test?foo=1", "/test"},
		{"/simple", "/simple"},
	}

	for _, tc := range cases {
		if got := aiko.EndpointFromURL(tc.input); got != tc.expected {
			t.Fatalf("expected %q from %q, got %q", tc.expected, tc.input, got)
		}
	}
}

func TestSignMatchesKnownVectors(t *testing.T) {
	cases := []struct {
		secret   string
		payload  string
		expected string
	}{
		{
			secret:   "djlXfG5bui6WOxCegNYl3C45d03r8z8gj3Tj3q9JCWA",
			payload:  "hello",
			expected: "961de8beee5e76cda1b594f78477dd108e67eb3b90690839b1bcc65eae6d678a",
		},
		{
			secret:   "HtvkaOMkRUTRsOXUngykx44gV71YCApJWvIhJRM6nWc",
			payload:  "hello",
			expected: "9a1cf5e2c1481be12eb333f3072951532732a9c6c4deeedf07ec762cf3816d17",
		},
	}

	for _, tc := range cases {
		key, err := base64.RawURLEncoding.DecodeString(tc.secret)
		if err != nil {
			t.Fatalf("decode secret: %v", err)
		}
		sig := aiko.Sign(key, []byte(tc.payload))
		if sig != tc.expected {
			t.Fatalf("expected signature %q, got %q", tc.expected, sig)
		}
	}
}

func TestGzipEventRoundTrip(t *testing.T) {
	evt := aiko.Event{
		URL:             "http://example.com/api",
		Endpoint:        "/api?q=a",
		Method:          "GET",
		StatusCode:      200,
		RequestHeaders:  map[string]string{"content-type": "application/json"},
		RequestBody:     map[string]any{"a": 1.0},
		ResponseHeaders: map[string]string{"content-type": "application/json"},
		ResponseBody:    map[string]any{"ok": true},
		DurationMS:      10,
	}

	compressed, err := aiko.GzipEvent(evt)
	if err != nil {
		t.Fatalf("gzip event: %v", err)
	}

	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("new gzip reader: %v", err)
	}
	defer zr.Close()

	var decoded aiko.Event
	if err := json.NewDecoder(zr).Decode(&decoded); err != nil {
		t.Fatalf("decode gzipped event: %v", err)
	}

	if decoded.URL != evt.URL || decoded.Endpoint != evt.Endpoint || decoded.Method != evt.Method {
		t.Fatalf("event metadata mismatch: %#v vs %#v", decoded, evt)
	}
	if decoded.DurationMS != evt.DurationMS {
		t.Fatalf("expected duration %d, got %d", evt.DurationMS, decoded.DurationMS)
	}
}
