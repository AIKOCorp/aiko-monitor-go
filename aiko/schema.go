package aiko

import (
	"errors"
	"log"
	"net/http"
	"regexp"
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
	localEndpointPattern = regexp.MustCompile(`^http://(?:localhost|127\.0\.0\.1|\[::1\]):\d+/api/monitor/ingest$`)
)

type Config struct {
	ProjectKey string
	SecretKey  string
	Endpoint   string
	Enabled    *bool

	MaxConcurrentSends int
	QueueSize          int
	HTTPClient         *http.Client
	Logger             *log.Logger
}

type Event struct {
	URL             string            `json:"url"`
	Endpoint        string            `json:"endpoint"`
	Method          string            `json:"method"`
	StatusCode      int               `json:"status_code"`
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody     any               `json:"request_body"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    any               `json:"response_body"`
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
		return errors.New("endpoint must match http://localhost:PORT/api/monitor/ingest or be 'https://monitor.aikocorp.ai/api/ingest' or 'https://staging.aikocorp.ai/api/monitor/ingest'")
	}
	return nil
}

func RedactEvent(evt Event) Event {
	return Event{
		URL:             evt.URL,
		Endpoint:        evt.Endpoint,
		Method:          evt.Method,
		StatusCode:      evt.StatusCode,
		RequestHeaders:  redactHeaders(evt.RequestHeaders),
		RequestBody:     RedactValue(evt.RequestBody),
		ResponseHeaders: redactHeaders(evt.ResponseHeaders),
		ResponseBody:    RedactValue(evt.ResponseBody),
		DurationMS:      evt.DurationMS,
	}
}
