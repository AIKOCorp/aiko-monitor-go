package aiko

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"regexp"
	"sync"
	"syscall"
	"time"
)

const (
	defaultEndpoint           = "https://main.aikocorp.ai/api/monitor/ingest"
	stagingEndpoint           = "https://staging.aikocorp.ai/api/monitor/ingest"
	sdkLanguage               = "go"
	defaultMaxConcurrentSends = 5
)

// sdkVersion can be overridden via ldflags at build time.
var sdkVersion = "dev"

var (
	projectKeyPattern    = regexp.MustCompile(`^pk_[A-Za-z0-9_-]{22}$`)
	localEndpointPattern = regexp.MustCompile(`^http://(?:localhost|127\.0\.0\.1|\[::1\]):\d+/api/monitor/ingest$`)
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Config defines the monitor configuration.
type Config struct {
	ProjectKey string
	SecretKey  string
	Endpoint   string
	Enabled    *bool
}

// Monitor is the central entry point for publishing HTTP events to Aiko.
type Monitor struct {
	cfg     Config
	secret  []byte
	client  *http.Client
	events  chan Event
	sem     chan struct{}
	wg      sync.WaitGroup
	once    sync.Once
	closeCh chan struct{}
	enabled bool
}

// Init constructs a new Monitor using the provided configuration.
func Init(cfg Config) (*Monitor, error) {
	if cfg.ProjectKey == "" {
		return nil, errors.New("project key required")
	}
	if cfg.SecretKey == "" {
		return nil, errors.New("secret key required")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}

	if err := validateConfig(cfg.ProjectKey, cfg.SecretKey, endpoint); err != nil {
		return nil, err
	}

	secret, err := base64.RawURLEncoding.DecodeString(cfg.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("decode secret key: %w", err)
	}

	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}

	m := &Monitor{
		cfg: Config{
			ProjectKey: cfg.ProjectKey,
			SecretKey:  cfg.SecretKey,
			Endpoint:   endpoint,
			Enabled:    cfg.Enabled,
		},
		secret:  secret,
		client:  &http.Client{Timeout: 10 * time.Second},
		events:  make(chan Event, 1024),
		sem:     make(chan struct{}, defaultMaxConcurrentSends),
		closeCh: make(chan struct{}),
		enabled: enabled,
	}

	if m.enabled {
		m.wg.Add(1)
		go m.run()
	}

	return m, nil
}

func validateConfig(projectKey, secretKey, endpoint string) error {
	if !projectKeyPattern.MatchString(projectKey) {
		return errors.New("projectKey must start with 'pk_' followed by 22 base64url characters")
	}
	if len(secretKey) != 43 {
		return errors.New("secretKey must be exactly 43 base64 characters")
	}
	if endpoint != defaultEndpoint && endpoint != stagingEndpoint && !localEndpointPattern.MatchString(endpoint) {
		return errors.New("endpoint must match http://localhost:PORT/api/monitor/ingest or be 'https://main.aikocorp.ai/api/monitor/ingest' or 'https://staging.aikocorp.ai/api/monitor/ingest'")
	}
	return nil
}

// AddEvent enqueues an event for delivery. Safe for concurrent use.
func (m *Monitor) AddEvent(evt Event) {
	if m == nil || !m.enabled {
		return
	}
	select {
	case m.events <- evt:
	case <-m.closeCh:
	default:
		log.Println("[Aiko] Event queue is full. Dropping event.")
	}
}

// Shutdown flushes pending events and releases resources.
func (m *Monitor) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}

	m.once.Do(func() {
		close(m.closeCh)
		if m.enabled {
			close(m.events)
		}
	})

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Monitor) run() {
	defer m.wg.Done()
	for evt := range m.events {
		m.sem <- struct{}{}
		m.wg.Add(1)
		go func(e Event) {
			defer m.wg.Done()
			defer func() { <-m.sem }()
			m.send(e)
		}(evt)
	}
}

func (m *Monitor) send(evt Event) {
	sanitized := redactEvent(evt)
	payload, err := gzipEvent(sanitized)
	if err != nil {
		return
	}

	signature := sign(m.secret, payload)

	const (
		maxAttempts    = 3
		baseBackoff    = 250 * time.Millisecond
		maxBackoff     = 2 * time.Second
		requestTimeout = 10 * time.Second
	)

	backoff := baseBackoff
	for attempt := 0; attempt < maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.Endpoint, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", "gzip")
		req.Header.Set("X-Project-Key", m.cfg.ProjectKey)
		req.Header.Set("X-Signature", signature)

		resp, err := m.client.Do(req)
		cancel()
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return
			}
			if !isRetryableStatus(resp.StatusCode) || attempt == maxAttempts-1 {
				return
			}
		} else if !isRetryableError(err) || attempt == maxAttempts-1 {
			return
		}

		time.Sleep(applyJitter(backoff))
		if backoff < maxBackoff {
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
		}
	}
}

func isRetryableStatus(status int) bool {
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500 && status < 600
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.Temporary() || dnsErr.IsTimeout
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return true
		}
		switch {
		case errors.Is(opErr.Err, syscall.ECONNREFUSED),
			errors.Is(opErr.Err, syscall.ECONNRESET),
			errors.Is(opErr.Err, syscall.ECONNABORTED),
			errors.Is(opErr.Err, syscall.EHOSTUNREACH),
			errors.Is(opErr.Err, syscall.ENETUNREACH):
			return true
		}

		var nestedDNS *net.DNSError
		if errors.As(opErr.Err, &nestedDNS) {
			return nestedDNS.Temporary() || nestedDNS.IsTimeout
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	return false
}

func applyJitter(base time.Duration) time.Duration {
	return time.Duration(float64(base) * (0.8 + 0.4*rand.Float64()))
}

func gzipEvent(evt Event) ([]byte, error) {
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

func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Event represents a single HTTP request/response lifecycle observation.
type Event struct {
	URL             string            `json:"url"`
	Endpoint        string            `json:"endpoint"`
	Method          string            `json:"method"`
	StatusCode      int               `json:"status_code"`
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody     interface{}       `json:"request_body"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    interface{}       `json:"response_body"`
	DurationMS      int64             `json:"duration_ms"`
}

func redactEvent(evt Event) Event {
	return Event{
		URL:             evt.URL,
		Endpoint:        evt.Endpoint,
		Method:          evt.Method,
		StatusCode:      evt.StatusCode,
		RequestHeaders:  redactHeaders(evt.RequestHeaders),
		RequestBody:     redactValue(evt.RequestBody),
		ResponseHeaders: redactHeaders(evt.ResponseHeaders),
		ResponseBody:    redactValue(evt.ResponseBody),
		DurationMS:      evt.DurationMS,
	}
}
