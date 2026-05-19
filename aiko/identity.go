package aiko

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/valyala/fasthttp"
)

func (m *Monitor) actorFromHTTPRequest(r *http.Request) *ActorContext {
	if m == nil || r == nil || !actorConfigured(m.cfg.Actor) {
		return nil
	}
	headers := CanonicalHeaders(r.Header)
	return m.resolveActor(ActorResolveContext{
		Headers:     headers,
		Cookies:     cookiesFromHTTPRequest(r),
		HTTPRequest: r,
	})
}

func (m *Monitor) actorFromFastHTTP(ctx *fasthttp.RequestCtx) *ActorContext {
	if m == nil || ctx == nil || !actorConfigured(m.cfg.Actor) {
		return nil
	}
	headers := CanonicalFastHTTPHeaders(ctx.Request.Header.All())
	return m.resolveActor(ActorResolveContext{
		Headers:            headers,
		Cookies:            cookiesFromFastHTTPRequest(ctx),
		FastHTTPRequestCtx: ctx,
	})
}

func (m *Monitor) resolveActor(ctx ActorResolveContext) *ActorContext {
	cfg := normalizeActorConfig(m.cfg.Actor)
	if cfg.Provider == ActorProviderCustom {
		return m.resolveCustomActor(cfg, ctx)
	}
	return m.resolveTokenActor(cfg, ctx)
}

func (m *Monitor) resolveCustomActor(cfg ActorConfig, ctx ActorResolveContext) *ActorContext {
	if cfg.Resolve == nil {
		m.verbosef("actor omitted provider=custom reason=missing_resolver")
		return nil
	}
	actor, err := cfg.Resolve(ctx)
	if err != nil {
		m.verbosef("actor omitted provider=custom reason=resolver_error error=%s", err)
		return nil
	}
	actor = normalizeCustomActor(actor)
	if actor == nil {
		m.verbosef("actor omitted provider=custom reason=resolver_empty")
		return nil
	}
	m.verbosef(
		"actor resolved provider=%s id=%s email=%s org_id=%s",
		actor.Provider,
		present(actor.ID != ""),
		present(actor.Email != ""),
		present(actor.OrgID != ""),
	)
	return actor
}

func (m *Monitor) resolveTokenActor(cfg ActorConfig, ctx ActorResolveContext) *ActorContext {
	carrierType, carrierName, carrierValue := actorCarrierValue(cfg, ctx)
	extractorType := actorExtractorType(cfg)
	m.verbosef(
		"actor configured provider=%s carrier=%s carrier_name=%s extractor=%s claim_id=%s claim_email=%s claim_org_id=%s",
		cfg.Provider,
		carrierType,
		carrierName,
		extractorType,
		cfg.Claims.ID,
		cfg.Claims.Email,
		cfg.Claims.OrgID,
	)

	token := tokenFromActorCarrier(cfg, carrierValue)
	if token == "" {
		m.verbosef("actor omitted provider=%s reason=missing_token carrier=%s carrier_name=%s", cfg.Provider, carrierType, carrierName)
		return nil
	}

	claims, ok := decodeJWTClaims(token)
	if !ok {
		m.verbosef("actor omitted provider=%s reason=invalid_jwt carrier=%s carrier_name=%s", cfg.Provider, carrierType, carrierName)
		return nil
	}

	actor := &ActorContext{
		Provider: cfg.Provider,
		ID:       stringAtPath(claims, cfg.Claims.ID),
		Email:    stringAtPath(claims, cfg.Claims.Email),
		OrgID:    stringAtPath(claims, cfg.Claims.OrgID),
	}
	if actor.ID == "" && actor.Email == "" && actor.OrgID == "" {
		m.verbosef("actor omitted provider=%s reason=claims_unresolved", cfg.Provider)
		return nil
	}
	m.verbosef(
		"actor resolved provider=%s id=%s email=%s org_id=%s",
		actor.Provider,
		present(actor.ID != ""),
		present(actor.Email != ""),
		present(actor.OrgID != ""),
	)
	return actor
}

func redactActorCarrierHeaders(headers map[string]string, cfg ActorConfig) {
	cfg = normalizeActorConfig(cfg)
	if cfg.Token.Header != nil {
		name := strings.ToLower(cfg.Token.Header.Name)
		if _, ok := headers[name]; ok {
			headers[name] = redactionMask
		}
	}
	if cfg.Token.Cookie != nil {
		if _, ok := headers["cookie"]; ok {
			headers["cookie"] = redactionMask
		}
	}
}

func actorCarrierValue(cfg ActorConfig, ctx ActorResolveContext) (string, string, string) {
	if cfg.Token.Header != nil {
		name := strings.ToLower(cfg.Token.Header.Name)
		return "header", name, ctx.Headers[name]
	}
	if cfg.Token.Cookie != nil {
		name := cfg.Token.Cookie.Name
		return "cookie", name, cookieValue(ctx, name)
	}
	return "", "", ""
}

func actorExtractorType(cfg ActorConfig) string {
	if cfg.Provider == ActorProviderSupabase {
		return "none"
	}
	if cfg.Token.Header != nil {
		return string(cfg.Token.Header.Extract.Type)
	}
	if cfg.Token.Cookie != nil {
		return string(cfg.Token.Cookie.Extract.Type)
	}
	return ""
}

func tokenFromActorCarrier(cfg ActorConfig, value string) string {
	switch cfg.Provider {
	case ActorProviderJWT:
		var extract ActorTokenExtractConfig
		if cfg.Token.Header != nil {
			extract = cfg.Token.Header.Extract
		} else if cfg.Token.Cookie != nil {
			extract = cfg.Token.Cookie.Extract
		}
		return tokenFromExtract(value, extract)
	case ActorProviderSupabase:
		return tokenFromSupabaseCookie(value)
	default:
		return ""
	}
}

func tokenFromExtract(value string, extract ActorTokenExtractConfig) string {
	switch extract.Type {
	case ActorTokenExtractTypeBearer:
		return tokenFromBearer(value)
	case ActorTokenExtractTypeRaw:
		return normalizeTokenString(urlDecode(value))
	case ActorTokenExtractTypeJSON:
		return tokenFromJSONValue(value, extract.Path)
	default:
		return ""
	}
}

func tokenFromBearer(value string) string {
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

func tokenFromJSONValue(value string, path string) string {
	var parsed any
	if err := json.Unmarshal([]byte(urlDecode(value)), &parsed); err != nil {
		return ""
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return ""
	}
	return normalizeTokenString(stringAtPath(object, path))
}

func tokenFromSupabaseCookie(value string) string {
	for _, candidate := range candidateValues(value) {
		if token := findAccessToken(candidate); token != "" {
			return token
		}
	}
	return ""
}

func candidateValues(value string) []any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	values := []string{value}
	decoded := urlDecode(value)
	if decoded != value {
		values = append(values, decoded)
	}

	out := make([]any, 0, len(values)*3)
	for _, candidate := range values {
		out = append(out, candidate)
		if strings.HasPrefix(candidate, "base64-") {
			if decodedJSON := decodeBase64Text(strings.TrimPrefix(candidate, "base64-")); decodedJSON != "" {
				out = append(out, decodedJSON)
			}
		}
		var parsed any
		if err := json.Unmarshal([]byte(candidate), &parsed); err == nil {
			out = append(out, parsed)
		}
	}
	return out
}

func findAccessToken(value any) string {
	switch cast := value.(type) {
	case string:
		token := normalizeTokenString(cast)
		if looksLikeJWT(token) {
			return token
		}
		for _, candidate := range candidateValues(cast) {
			if candidateString, ok := candidate.(string); ok && candidateString == cast {
				continue
			}
			if token := findAccessToken(candidate); token != "" {
				return token
			}
		}
	case map[string]any:
		if value, ok := cast["access_token"].(string); ok {
			return normalizeTokenString(value)
		}
		for _, nested := range cast {
			if token := findAccessToken(nested); token != "" {
				return token
			}
		}
	case []any:
		for _, nested := range cast {
			if token := findAccessToken(nested); token != "" {
				return token
			}
		}
	}
	return ""
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

func normalizeCustomActor(actor *ActorContext) *ActorContext {
	if actor == nil {
		return nil
	}
	out := *actor
	out.Provider = ActorProvider(strings.TrimSpace(string(out.Provider)))
	if out.Provider == "" {
		out.Provider = ActorProviderCustom
	}
	out.ID = strings.TrimSpace(out.ID)
	out.Email = strings.TrimSpace(out.Email)
	out.OrgID = strings.TrimSpace(out.OrgID)
	if out.ID == "" && out.Email == "" && out.OrgID == "" {
		return nil
	}
	return &out
}

func cookiesFromHTTPRequest(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, cookie := range r.Cookies() {
		out[cookie.Name] = cookie.Value
	}
	return out
}

func cookiesFromFastHTTPRequest(ctx *fasthttp.RequestCtx) map[string]string {
	out := map[string]string{}
	ctx.Request.Header.VisitAllCookie(func(key, value []byte) {
		out[string(key)] = string(value)
	})
	return out
}

func cookieValue(ctx ActorResolveContext, name string) string {
	cookies := map[string]string{}
	for key, value := range ctx.Cookies {
		cookies[key] = value
	}
	for key, value := range parseCookieHeader(ctx.Headers["cookie"]) {
		cookies[key] = value
	}
	if value, ok := cookies[name]; ok {
		return value
	}

	prefix := name + "."
	indexes := make([]int, 0)
	chunks := map[int]string{}
	for key, value := range cookies {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		idx, err := strconv.Atoi(strings.TrimPrefix(key, prefix))
		if err != nil {
			continue
		}
		indexes = append(indexes, idx)
		chunks[idx] = value
	}
	sort.Ints(indexes)
	var b strings.Builder
	for _, idx := range indexes {
		b.WriteString(chunks[idx])
	}
	return b.String()
}

func parseCookieHeader(value string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(value, ";") {
		key, raw, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			out[key] = strings.TrimSpace(raw)
		}
	}
	return out
}

func normalizeTokenString(value string) string {
	value = strings.TrimSpace(value)
	if token := tokenFromBearer(value); token != "" {
		return token
	}
	return value
}

func looksLikeJWT(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	return parts[0] != "" && parts[1] != "" && parts[2] != ""
}

func urlDecode(value string) string {
	decoded, err := url.QueryUnescape(strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	return decoded
}

func decodeBase64Text(value string) string {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		raw, err = base64.StdEncoding.DecodeString(value)
		if err != nil {
			return ""
		}
	}
	return string(raw)
}

func present(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}
