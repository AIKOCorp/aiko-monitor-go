package aiko

import (
	"encoding/base64"
	"regexp"
	"strings"
)

const redactionMask = "[REDACTED]"

var sensitiveKeys = map[string]struct{}{
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

var piiPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[\w\.-]+@[\w\.-]+\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
	regexp.MustCompile(`(?i)\b(?:[A-F0-9]{1,4}:){2,7}[A-F0-9]{1,4}\b`),
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
		out[lower] = redactString(v)
	}
	return out
}

func redactValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, val := range v {
			if _, ok := sensitiveKeys[strings.ToLower(key)]; ok {
				out[key] = redactionMask
			} else {
				out[key] = redactValue(val)
			}
		}
		return out
	case map[string]string:
		out := make(map[string]interface{}, len(v))
		for key, val := range v {
			if _, ok := sensitiveKeys[strings.ToLower(key)]; ok {
				out[key] = redactionMask
			} else {
				out[key] = redactValue(val)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = redactValue(item)
		}
		return out
	case []string:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = redactValue(item)
		}
		return out
	case string:
		return redactString(v)
	case []byte:
		encoded := base64.StdEncoding.EncodeToString(v)
		return map[string]string{"base64": encoded}
	default:
		return value
	}
}

func redactString(s string) string {
	masked := s
	for _, rx := range piiPatterns {
		masked = rx.ReplaceAllString(masked, redactionMask)
	}
	return masked
}
