package aiko

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
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

func tryParseJSON(raw []byte) (any, bool) {
	var out any
	if err := json.Unmarshal(raw, &out); err == nil {
		return out, true
	}
	return nil, false
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
