package aiko

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func normalizeHeaders(h http.Header) map[string]string {
	if h == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(h))
	for key, values := range h {
		lower := strings.ToLower(key)
		if len(values) == 0 {
			continue
		}
		out[lower] = strings.Join(values, ", ")
	}
	return out
}

func parseJSONBody(raw []byte) interface{} {
	if len(raw) == 0 {
		return map[string]interface{}{}
	}
	var out interface{}
	if err := json.Unmarshal(raw, &out); err == nil {
		return out
	}
	return string(raw)
}

func decodeResponseBody(raw []byte, headers map[string]string) interface{} {
	if len(raw) == 0 {
		return map[string]interface{}{}
	}

	decoded := raw
	encoding := strings.ToLower(headers["content-encoding"])
	switch {
	case strings.Contains(encoding, "gzip"):
		if gr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
			if data, derr := io.ReadAll(gr); derr == nil {
				decoded = data
			}
			gr.Close()
		}
	case strings.Contains(encoding, "deflate"):
		if fr := flate.NewReader(bytes.NewReader(raw)); fr != nil {
			if data, err := io.ReadAll(fr); err == nil {
				decoded = data
			}
			fr.Close()
		}
	}

	ctype := strings.ToLower(headers["content-type"])
	switch {
	case strings.Contains(ctype, "application/json"):
		return parseJSONBody(decoded)
	case strings.HasPrefix(ctype, "text/") || strings.Contains(ctype, "xml") || strings.Contains(ctype, "html"):
		return string(decoded)
	default:
		return map[string]string{"base64": base64.StdEncoding.EncodeToString(decoded)}
	}
}
