package aiko

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

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

func CanonicalHeaderMap(headers map[string]string) map[string]string {
	if headers == nil {
		return map[string]string{}
	}

	normalized := make(map[string]string, len(headers))
	for key, value := range headers {
		if key == "" {
			continue
		}
		lower := strings.ToLower(key)
		if existing, ok := normalized[lower]; ok {
			switch {
			case existing == "":
				normalized[lower] = value
			case value == "" || value == existing:
				normalized[lower] = existing
			default:
				normalized[lower] = existing + ", " + value
			}
		} else {
			normalized[lower] = value
		}
	}

	if len(normalized) == 0 {
		return map[string]string{}
	}
	return normalized
}

func ParseJSONBody(raw []byte) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out any
	if err := json.Unmarshal(raw, &out); err == nil {
		return out
	}
	return string(raw)
}

func DecodeResponseBody(raw []byte, headers map[string]string) any {
	if len(raw) == 0 {
		return map[string]any{}
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

func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func GzipEvent(evt Event) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	enc := json.NewEncoder(gz)
	if err := enc.Encode(evt); err != nil {
		if closeErr := gz.Close(); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func VersionHeaderValue() string {
	// figure out a way to set the version automatically from git tag
	return fmt.Sprintf("go:%s", "0.0.3")
}

func decodeWithEncoding(raw []byte, encoding string) []byte {
	lower := strings.ToLower(encoding)

	if strings.Contains(lower, "gzip") {
		if gr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
			data, derr := io.ReadAll(gr)
			if cerr := gr.Close(); cerr != nil {
				return raw
			}
			if derr == nil {
				return data
			}
		}
	}

	if strings.Contains(lower, "deflate") {
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			data, derr := io.ReadAll(zr)
			if cerr := zr.Close(); cerr != nil {
				return raw
			}
			if derr == nil {
				return data
			}
		}
		if fr := flate.NewReader(bytes.NewReader(raw)); fr != nil {
			data, err := io.ReadAll(fr)
			if cerr := fr.Close(); cerr != nil {
				return raw
			}
			if err == nil {
				return data
			}
		}
	}

	return raw
}

func tryParseJSON(raw []byte) (any, bool) {
	var out any
	if err := json.Unmarshal(raw, &out); err == nil {
		return out, true
	}
	return nil, false
}
