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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const redactionMask = "[REDACTED]"

var (
	sensitiveKeys = map[string]struct{}{
		"password":         {},
		"secret":           {},
		"token":            {},
		"api_key":          {},
		"authorization":    {},
		"cookie":           {},
		"email":            {},
		"phonenumber":      {},
		"ssn":              {},
		"creditcard":       {},
		"set-cookie":       {},
		"ip":               {},
		"x-forwarded-for":  {},
		"x-forwarded-ip":   {},
		"x-real-ip":        {},
		"cf-connecting-ip": {},
		"true-client-ip":   {},
		"forwarded":        {},
		"remote-addr":      {},
		"client-ip":        {},
	}

	piiPatterns = []*regexp.Regexp{
		regexp.MustCompile(`[\w\.-]+@[\w\.-]+\.[A-Za-z]{2,}`),
		regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
		regexp.MustCompile(`(?i)\b(?:[A-F0-9]{1,4}:){2,7}[A-F0-9]{1,4}\b`),
	}
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

func RedactValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, val := range v {
			if _, ok := sensitiveKeys[strings.ToLower(key)]; ok {
				out[key] = redactionMask
				continue
			}
			out[key] = RedactValue(val)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(v))
		for key, val := range v {
			if _, ok := sensitiveKeys[strings.ToLower(key)]; ok {
				out[key] = redactionMask
				continue
			}
			out[key] = RedactValue(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = RedactValue(item)
		}
		return out
	case []string:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = RedactValue(item)
		}
		return out
	case string:
		return RedactString(v)
	case []byte:
		encoded := base64.StdEncoding.EncodeToString(v)
		return map[string]string{"base64": encoded}
	default:
		return value
	}
}

func RedactString(s string) string {
	masked := s
	for _, rx := range piiPatterns {
		masked = rx.ReplaceAllString(masked, redactionMask)
	}
	return masked
}

func EndpointFromURL(raw string) string {
	if raw == "" {
		return ""
	}

	path := raw

	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") {
		if u, err := urlParse(raw); err == nil {
			path = preferredPath(u)
		}
	} else if strings.HasPrefix(raw, "/") {
		path = raw
	} else if u, err := urlParse(raw); err == nil {
		candidate := preferredPath(u)
		if candidate != "" {
			path = candidate
		}
	}

	path = trimQuery(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
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
		gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const sdkLanguage = "go"

var sdkVersion = "dev"

func SDKVersion() string {
	return sdkVersion
}

func VersionHeaderValue() string {
	return fmt.Sprintf("%s:%s", sdkLanguage, sdkVersion)
}

func redactHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		lower := strings.ToLower(k)
		if _, ok := sensitiveKeys[lower]; ok {
			out[lower] = redactionMask
			continue
		}
		out[lower] = RedactString(v)
	}
	return out
}

func decodeWithEncoding(raw []byte, encoding string) []byte {
	lower := strings.ToLower(encoding)

	if strings.Contains(lower, "gzip") {
		if gr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
			data, derr := io.ReadAll(gr)
			gr.Close()
			if derr == nil {
				return data
			}
		}
	}

	if strings.Contains(lower, "deflate") {
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			data, derr := io.ReadAll(zr)
			zr.Close()
			if derr == nil {
				return data
			}
		}
		if fr := flate.NewReader(bytes.NewReader(raw)); fr != nil {
			data, err := io.ReadAll(fr)
			fr.Close()
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

func preferredPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.EscapedPath() != "" {
		return u.EscapedPath()
	}
	if u.Path != "" {
		return u.Path
	}
	return ""
}

func trimQuery(raw string) string {
	if idx := strings.IndexByte(raw, '?'); idx >= 0 {
		return raw[:idx]
	}
	return raw
}

func urlParse(raw string) (*url.URL, error) {
	return url.Parse(raw)
}
