package aiko

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// CanonicalHeaders flattens HTTP headers into lowercase keys with comma-joined values.
func CanonicalHeaders(h http.Header) map[string]string {
	if h == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(h))
	for key, values := range h {
		if len(values) == 0 {
			continue
		}
		out[strings.ToLower(key)] = strings.Join(values, ", ")
	}
	return out
}

// ParseJSONBody returns a decoded JSON representation or a best-effort fallback.
func ParseJSONBody(raw []byte) interface{} {
	if len(raw) == 0 {
		return map[string]interface{}{}
	}
	var out interface{}
	if err := json.Unmarshal(raw, &out); err == nil {
		return out
	}
	return string(raw)
}

// DecodeResponseBody interprets a response payload based on headers and encoding.
func DecodeResponseBody(raw []byte, headers map[string]string) interface{} {
	if len(raw) == 0 {
		return map[string]interface{}{}
	}

	decoded := decodeWithEncoding(raw, strings.ToLower(headers["content-encoding"]))
	ctype := strings.ToLower(headers["content-type"])

	if strings.Contains(ctype, "application/json") {
		if parsed, ok := tryParseJSON(decoded); ok {
			return parsed
		}
		return string(decoded)
	}

	if strings.HasPrefix(ctype, "text/") || strings.Contains(ctype, "xml") || strings.Contains(ctype, "html") {
		return string(decoded)
	}

	if ctype != "" {
		if parsed, ok := tryParseJSON(decoded); ok {
			return parsed
		}
		return map[string]string{"base64": base64.StdEncoding.EncodeToString(decoded)}
	}

	if parsed, ok := tryParseJSON(decoded); ok {
		return parsed
	}
	if utf8.Valid(decoded) {
		return string(decoded)
	}
	return map[string]string{"base64": base64.StdEncoding.EncodeToString(decoded)}
}

func tryParseJSON(raw []byte) (interface{}, bool) {
	var out interface{}
	if err := json.Unmarshal(raw, &out); err == nil {
		return out, true
	}
	return nil, false
}

func decodeWithEncoding(raw []byte, encoding string) []byte {
	decoded := raw
	switch {
	case strings.Contains(encoding, "gzip"):
		if gr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
			if data, derr := io.ReadAll(gr); derr == nil {
				decoded = data
			}
			gr.Close()
		}
	case strings.Contains(encoding, "deflate"):
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			if data, derr := io.ReadAll(zr); derr == nil {
				decoded = data
			}
			zr.Close()
		}
	}
	return decoded
}
