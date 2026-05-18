package aiko

import (
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
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

const ActorProviderJWT ActorProvider = "jwt"

type ActorConfig struct {
	Provider   ActorProvider
	IDClaim    string
	EmailClaim string
}

type ActorContext struct {
	Provider ActorProvider `json:"provider,omitempty"`
	ID       string        `json:"id,omitempty"`
	Email    string        `json:"email,omitempty"`
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
	hasIDClaim := strings.TrimSpace(cfg.IDClaim) != ""
	hasEmailClaim := strings.TrimSpace(cfg.EmailClaim) != ""
	if (hasIDClaim || hasEmailClaim) && !hasProvider {
		return errors.New("actor.provider is required when actor claim paths are configured")
	}
	if hasProvider && !hasIDClaim && !hasEmailClaim {
		return errors.New("actor requires at least one of id claim or email claim")
	}
	if hasProvider && ActorProvider(strings.ToLower(strings.TrimSpace(string(cfg.Provider)))) != ActorProviderJWT {
		return errors.New("actor.provider must be jwt")
	}
	return nil
}

func normalizeActorConfig(cfg ActorConfig) ActorConfig {
	return ActorConfig{
		Provider:   ActorProvider(strings.ToLower(strings.TrimSpace(string(cfg.Provider)))),
		IDClaim:    strings.TrimSpace(cfg.IDClaim),
		EmailClaim: strings.TrimSpace(cfg.EmailClaim),
	}
}

func actorJWTConfigured(cfg ActorConfig) bool {
	cfg = normalizeActorConfig(cfg)
	return cfg.Provider == ActorProviderJWT && (cfg.IDClaim != "" || cfg.EmailClaim != "")
}
