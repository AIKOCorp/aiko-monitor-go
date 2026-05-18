package aiko

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/valyala/fasthttp"
)

func (m *Monitor) actorFromHTTPRequest(r *http.Request) *ActorContext {
	if m == nil || r == nil || !actorJWTConfigured(m.cfg.Actor) {
		return nil
	}
	return actorFromJWTToken(m.cfg.Actor, tokenFromBearerHeader(r.Header.Get("Authorization")))
}

func (m *Monitor) actorFromFastHTTP(ctx *fasthttp.RequestCtx) *ActorContext {
	if m == nil || ctx == nil || !actorJWTConfigured(m.cfg.Actor) {
		return nil
	}
	return actorFromJWTToken(m.cfg.Actor, tokenFromBearerHeader(string(ctx.Request.Header.Peek("Authorization"))))
}

func tokenFromBearerHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(value[len(prefix):])
}

func actorFromJWTToken(cfg ActorConfig, token string) *ActorContext {
	cfg = normalizeActorConfig(cfg)
	claims, ok := decodeJWTClaims(token)
	if !ok {
		return nil
	}
	actor := &ActorContext{
		Provider: cfg.Provider,
		ID:       stringAtPath(claims, cfg.IDClaim),
		Email:    stringAtPath(claims, cfg.EmailClaim),
	}
	if actor.ID == "" && actor.Email == "" {
		return nil
	}
	return actor
}

func decodeJWTClaims(token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var claims map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		return nil, false
	}
	return claims, true
}

func stringAtPath(claims map[string]any, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if value, ok := claims[path]; ok {
		return stringValue(value)
	}
	var current any = claims
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return ""
		}
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = object[part]
		if !ok {
			return ""
		}
	}
	return stringValue(current)
}

func stringValue(value any) string {
	switch cast := value.(type) {
	case string:
		return strings.TrimSpace(cast)
	case json.Number:
		return strings.TrimSpace(cast.String())
	case float64, bool:
		return strings.TrimSpace(fmt.Sprint(cast))
	default:
		return ""
	}
}
