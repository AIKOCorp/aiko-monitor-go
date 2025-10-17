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
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

const defaultEndpoint = "https://main.aikocorp.ai/api/monitor/ingest"
const sdkLanguage = "go"

// sdkVersion can be overridden via ldflags at build time.
var sdkVersion = "dev"

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
		closeCh: make(chan struct{}),
		enabled: enabled,
	}

	if m.enabled {
		m.wg.Add(1)
		go m.run()
	}

	return m, nil
}

// AddEvent enqueues an event for delivery. Safe for concurrent use.
func (m *Monitor) AddEvent(evt Event) {
	if m == nil || !m.enabled {
		return
	}
	select {
	case m.events <- evt:
	case <-m.closeCh:
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
		m.send(evt)
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
	var netErr interface{ Temporary() bool }
	if errors.As(err, &netErr) {
		return netErr.Temporary()
	}
	return true
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
