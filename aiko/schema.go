package aiko

import (
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
)

const (
	defaultEndpoint           = "https://monitor.aikocorp.ai/api/ingest"
	stagingEndpoint           = "https://staging.aikocorp.ai/api/monitor/ingest"
	defaultMaxConcurrentSends = 5
	defaultQueueSize          = 5000
	defaultHTTPTimeout        = 10 * time.Second
)

var (
	projectKeyPattern    = regexp.MustCompile(`^pk_[A-Za-z0-9_-]{22}$`)
	localEndpointPattern = regexp.MustCompile(`^http://(?:localhost|127\.0\.0\.1|\[::1\]):\d+/api/ingest$`)
)

type Config struct {
	ProjectKey string
	SecretKey  string
	Endpoint   string
	Enabled    *bool
	Verbose    bool
	Actor      ActorConfig

	MaxConcurrentSends int
	QueueSize          int
	HTTPClient         *http.Client
	Logger             *log.Logger
}

type ActorProvider string

const (
	ActorProviderJWT      ActorProvider = "jwt"
	ActorProviderSupabase ActorProvider = "supabase"
	ActorProviderCustom   ActorProvider = "custom"
)

type ActorTokenExtractType string

const (
	ActorTokenExtractTypeBearer ActorTokenExtractType = "bearer"
	ActorTokenExtractTypeRaw    ActorTokenExtractType = "raw"
	ActorTokenExtractTypeJSON   ActorTokenExtractType = "json"
)

type ActorConfig struct {
	Provider ActorProvider
	Token    ActorTokenConfig
	Claims   ActorClaimsConfig
	Resolve  ActorResolver
}

type ActorTokenConfig struct {
	Header *ActorHeaderTokenConfig
	Cookie *ActorCookieTokenConfig
}

type ActorHeaderTokenConfig struct {
	Name    string
	Extract ActorTokenExtractConfig
}

type ActorCookieTokenConfig struct {
	Name    string
	Extract ActorTokenExtractConfig
}

type ActorTokenExtractConfig struct {
	Type ActorTokenExtractType
	Path string
}

type ActorClaimsConfig struct {
	ID    string
	Email string
	OrgID string
}

type ActorResolveContext struct {
	Headers            map[string]string
	Cookies            map[string]string
	HTTPRequest        *http.Request
	FastHTTPRequestCtx *fasthttp.RequestCtx
}

type ActorResolver func(ActorResolveContext) (*ActorContext, error)

type ActorContext struct {
	Provider ActorProvider `json:"provider,omitempty"`
	ID       string        `json:"id,omitempty"`
	Email    string        `json:"email,omitempty"`
	OrgID    string        `json:"org_id,omitempty"`
}

func ActorTokenExtractBearer() ActorTokenExtractConfig {
	return ActorTokenExtractConfig{Type: ActorTokenExtractTypeBearer}
}

func ActorTokenExtractRaw() ActorTokenExtractConfig {
	return ActorTokenExtractConfig{Type: ActorTokenExtractTypeRaw}
}

func ActorTokenExtractJSON(path string) ActorTokenExtractConfig {
	return ActorTokenExtractConfig{Type: ActorTokenExtractTypeJSON, Path: path}
}

type Event struct {
	ID              string            `json:"id"`
	URL             string            `json:"url"`
	Endpoint        string            `json:"endpoint"`
	Method          string            `json:"method"`
	StatusCode      int               `json:"status_code"`
	Actor           *ActorContext     `json:"actor,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody     any               `json:"request_body"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    any               `json:"response_body"`
	Timestamp       string            `json:"timestamp,omitempty"`
	DurationMS      int64             `json:"duration_ms"`
}

func ValidateConfig(projectKey, secretKey, endpoint string) error {
	if !projectKeyPattern.MatchString(projectKey) {
		return errors.New("projectKey must start with 'pk_' followed by 22 base64url characters")
	}
	if len(secretKey) != 43 {
		return errors.New("secretKey must be exactly 43 base64url characters")
	}
	if endpoint != defaultEndpoint && endpoint != stagingEndpoint && !localEndpointPattern.MatchString(endpoint) {
		return errors.New("endpoint must match http://localhost:PORT/api/ingest or be 'https://monitor.aikocorp.ai/api/ingest' or 'https://staging.aikocorp.ai/api/monitor/ingest'")
	}
	return nil
}

func RedactEvent(evt Event) Event {
	return Event{
		ID:              evt.ID,
		URL:             evt.URL,
		Endpoint:        evt.Endpoint,
		Method:          evt.Method,
		StatusCode:      evt.StatusCode,
		Actor:           cloneActorContext(evt.Actor),
		RequestHeaders:  redactHeaders(evt.RequestHeaders),
		RequestBody:     RedactValue(evt.RequestBody),
		ResponseHeaders: redactHeaders(evt.ResponseHeaders),
		ResponseBody:    RedactValue(evt.ResponseBody),
		Timestamp:       evt.Timestamp,
		DurationMS:      evt.DurationMS,
	}
}

func cloneActorContext(actor *ActorContext) *ActorContext {
	if actor == nil {
		return nil
	}
	out := *actor
	return &out
}

func validateActorConfig(cfg ActorConfig) error {
	hasProvider := strings.TrimSpace(string(cfg.Provider)) != ""
	hasFields := actorTokenConfigured(cfg.Token) || actorClaimsConfigured(cfg.Claims) || cfg.Resolve != nil
	if hasFields && !hasProvider {
		return errors.New("actor.provider is required when actor fields are configured")
	}
	if !hasFields && !hasProvider {
		return nil
	}
	provider := ActorProvider(strings.ToLower(strings.TrimSpace(string(cfg.Provider))))
	if provider != ActorProviderJWT && provider != ActorProviderSupabase && provider != ActorProviderCustom {
		return errors.New("actor.provider must be jwt, supabase, or custom")
	}
	if provider == ActorProviderCustom {
		if cfg.Resolve == nil {
			return errors.New("actor.resolve is required when actor.provider is custom")
		}
		if actorTokenConfigured(cfg.Token) || actorClaimsConfigured(cfg.Claims) {
			return errors.New("actor.provider custom cannot define token or claims")
		}
		return nil
	}
	if cfg.Resolve != nil {
		return errors.New("actor.resolve is only valid when actor.provider is custom")
	}
	if !actorTokenConfigured(cfg.Token) {
		return errors.New("actor.token is required when actor.provider is configured")
	}
	if err := validateActorTokenConfig(provider, cfg.Token); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Claims.ID) == "" {
		return errors.New("actor.claims.id is required")
	}
	if strings.TrimSpace(cfg.Claims.Email) == "" {
		return errors.New("actor.claims.email is required")
	}
	return nil
}

func normalizeActorConfig(cfg ActorConfig) ActorConfig {
	return ActorConfig{
		Provider: ActorProvider(strings.ToLower(strings.TrimSpace(string(cfg.Provider)))),
		Token:    normalizeActorTokenConfig(cfg.Token),
		Claims: ActorClaimsConfig{
			ID:    strings.TrimSpace(cfg.Claims.ID),
			Email: strings.TrimSpace(cfg.Claims.Email),
			OrgID: strings.TrimSpace(cfg.Claims.OrgID),
		},
		Resolve: cfg.Resolve,
	}
}

func actorConfigured(cfg ActorConfig) bool {
	cfg = normalizeActorConfig(cfg)
	if cfg.Provider == ActorProviderCustom {
		return cfg.Resolve != nil
	}
	return (cfg.Provider == ActorProviderJWT || cfg.Provider == ActorProviderSupabase) &&
		actorTokenConfigured(cfg.Token) &&
		cfg.Claims.ID != "" &&
		cfg.Claims.Email != ""
}

func actorTokenConfigured(cfg ActorTokenConfig) bool {
	return cfg.Header != nil || cfg.Cookie != nil
}

func actorClaimsConfigured(cfg ActorClaimsConfig) bool {
	return strings.TrimSpace(cfg.ID) != "" || strings.TrimSpace(cfg.Email) != "" || strings.TrimSpace(cfg.OrgID) != ""
}

func validateActorTokenConfig(provider ActorProvider, cfg ActorTokenConfig) error {
	hasHeader := cfg.Header != nil
	hasCookie := cfg.Cookie != nil
	if hasHeader == hasCookie {
		return errors.New("actor.token requires exactly one of header or cookie")
	}
	if hasHeader {
		if strings.TrimSpace(cfg.Header.Name) == "" {
			return errors.New("actor.token.header.name is required")
		}
		if provider == ActorProviderSupabase {
			return errors.New("actor.provider supabase requires token.cookie")
		}
		if provider == ActorProviderJWT && !actorExtractConfigured(cfg.Header.Extract) {
			return errors.New("actor token extract is required when actor.provider is jwt")
		}
		return validateActorTokenExtractConfig(cfg.Header.Extract)
	}
	if strings.TrimSpace(cfg.Cookie.Name) == "" {
		return errors.New("actor.token.cookie.name is required")
	}
	if provider == ActorProviderSupabase {
		if actorExtractConfigured(cfg.Cookie.Extract) {
			return errors.New("actor.token.cookie.extract is not allowed when actor.provider is supabase")
		}
		return nil
	}
	if provider == ActorProviderJWT && !actorExtractConfigured(cfg.Cookie.Extract) {
		return errors.New("actor token extract is required when actor.provider is jwt")
	}
	return validateActorTokenExtractConfig(cfg.Cookie.Extract)
}

func validateActorTokenExtractConfig(cfg ActorTokenExtractConfig) error {
	extractType := ActorTokenExtractType(strings.ToLower(strings.TrimSpace(string(cfg.Type))))
	path := strings.TrimSpace(cfg.Path)
	switch extractType {
	case ActorTokenExtractTypeBearer, ActorTokenExtractTypeRaw:
		if path != "" {
			return errors.New("actor token extract path is only valid for json extract")
		}
	case ActorTokenExtractTypeJSON:
		if path == "" {
			return errors.New("actor token json extract path is required")
		}
	default:
		return errors.New("actor token extract type must be bearer, raw, or json")
	}
	return nil
}

func actorExtractConfigured(cfg ActorTokenExtractConfig) bool {
	return strings.TrimSpace(string(cfg.Type)) != "" || strings.TrimSpace(cfg.Path) != ""
}

func normalizeActorTokenConfig(cfg ActorTokenConfig) ActorTokenConfig {
	out := ActorTokenConfig{}
	if cfg.Header != nil {
		out.Header = &ActorHeaderTokenConfig{
			Name:    strings.ToLower(strings.TrimSpace(cfg.Header.Name)),
			Extract: normalizeActorTokenExtractConfig(cfg.Header.Extract),
		}
	}
	if cfg.Cookie != nil {
		out.Cookie = &ActorCookieTokenConfig{
			Name:    strings.TrimSpace(cfg.Cookie.Name),
			Extract: normalizeActorTokenExtractConfig(cfg.Cookie.Extract),
		}
	}
	return out
}

func normalizeActorTokenExtractConfig(cfg ActorTokenExtractConfig) ActorTokenExtractConfig {
	return ActorTokenExtractConfig{
		Type: ActorTokenExtractType(strings.ToLower(strings.TrimSpace(string(cfg.Type)))),
		Path: strings.TrimSpace(cfg.Path),
	}
}
