package aiko

import (
	"errors"
	"log"
	"net/http"
	"regexp"
	"time"
)

const (
	defaultEndpoint           = "https://main.aikocorp.ai/api/monitor/ingest"
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

func validateConfig(projectKey, secretKey, endpoint string) error {
	if !projectKeyPattern.MatchString(projectKey) {
		return errors.New("projectKey must start with 'pk_' followed by 22 base64url characters")
	}
	if len(secretKey) != 43 {
		return errors.New("secretKey must be exactly 43 base64url characters")
	}
	if endpoint != defaultEndpoint && endpoint != stagingEndpoint && !localEndpointPattern.MatchString(endpoint) {
		return errors.New("endpoint must match http://localhost:PORT/api/monitor/ingest or be 'https://main.aikocorp.ai/api/monitor/ingest' or 'https://staging.aikocorp.ai/api/monitor/ingest'")
	}
	return nil
}
