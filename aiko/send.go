package aiko

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Monitor struct {
	cfg          Config
	secret       []byte
	client       *http.Client
	logger       *log.Logger
	events       chan Event
	sem          chan struct{}
	wg           sync.WaitGroup
	once         sync.Once
	closeCh      chan struct{}
	enabled      bool
	rnd          *rand.Rand
	rndMu        sync.Mutex
	verifiedOnce sync.Once
}

const (
	maxAttempts    = 3
	baseBackoff    = 250 * time.Millisecond
	maxBackoff     = 2 * time.Second
	requestTimeout = 10 * time.Second
)

// kept for backward comptibility
func (m *Monitor) AddEvent(evt Event) {
	if m == nil || !m.enabled {
		return
	}

	m.addEvent(evt)
}

func (m *Monitor) addEvent(evt Event) {
	if m == nil || !m.enabled {
		return
	}

	evt = normalizeEvent(evt)
	select {
	case m.events <- evt:
		m.verbosef("queued event_id=%s queue_depth=%d queue_size=%d", evt.ID, len(m.events), cap(m.events))
	case <-m.closeCh:
	default:
		m.logger.Printf("aiko monitor queue is full; dropping event")
	} // shouldn't drop
}

func normalizeEvent(evt Event) Event {
	if evt.ID == "" {
		evt.ID = newEventID()
	}
	if evt.Timestamp == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	evt.Method = strings.ToUpper(evt.Method)
	evt.RequestHeaders = CanonicalHeaderMap(evt.RequestHeaders)
	evt.ResponseHeaders = CanonicalHeaderMap(evt.ResponseHeaders)
	return evt
}

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

	if !m.enabled {
		return nil
	}

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

func (m *Monitor) Close() error {
	return m.Shutdown(context.Background())
}

func (m *Monitor) Enabled() bool {
	if m == nil {
		return false
	}
	return m.enabled
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

func (m *Monitor) jitter(base time.Duration) time.Duration {
	if m.rnd == nil {
		return base
	}
	m.rndMu.Lock()
	factor := 0.8 + 0.4*m.rnd.Float64()
	m.rndMu.Unlock()
	return time.Duration(float64(base) * factor)
}

func (m *Monitor) send(evt Event) {
	evt = normalizeEvent(evt)
	peerIP := evt.RequestHeaders["x-aiko-peer-ip"]
	if peerIP != "" {
		delete(evt.RequestHeaders, "x-aiko-peer-ip")
	}
	clientIP := extractClientIP(evt.RequestHeaders, peerIP)
	sanitized := RedactEvent(evt)
	payload, err := GzipEvent(sanitized)
	if err != nil {
		return
	}

	signature := Sign(m.secret, payload)
	backoff := baseBackoff

	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptNumber := attempt + 1
		attemptCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, m.cfg.Endpoint, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", "gzip")
		req.Header.Set("X-Project-Key", m.cfg.ProjectKey)
		req.Header.Set("X-Signature", signature)
		if clientIP != "" {
			req.Header.Set("X-Client-IP", clientIP)
		}

		start := time.Now()
		m.verbosef(
			"send attempt event_id=%s attempt=%d max_attempts=%d method=%s endpoint=%s payload_bytes=%d",
			evt.ID,
			attemptNumber,
			maxAttempts,
			evt.Method,
			evt.Endpoint,
			len(payload),
		)
		resp, err := m.client.Do(req)
		latencyMS := time.Since(start).Milliseconds()
		nextDelay := m.jitter(backoff)

		if err == nil {
			if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil && m.logger != nil {
				m.logger.Printf("aiko: drain response body: %v", copyErr)
			}
			if closeErr := resp.Body.Close(); closeErr != nil && m.logger != nil {
				m.logger.Printf("aiko: close response body: %v", closeErr)
			}
			cancel()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				requestID := responseRequestID(resp.Header)
				m.verbosef(
					"send accepted event_id=%s status=%d request_id=%s latency_ms=%d",
					evt.ID,
					resp.StatusCode,
					requestID,
					latencyMS,
				)
				m.verifiedOnce.Do(func() {
					m.verbosef("install verified: monitor accepted first event")
				})
				return
			}
			if !isRetryableStatus(resp.StatusCode) || attempt == maxAttempts-1 {
				return
			}
		} else {
			cancel()
			if !IsRetryableError(err) || attempt == maxAttempts-1 {
				return
			}
		}

		time.Sleep(nextDelay)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func isRetryableStatus(status int) bool {
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500 && status < 600
}

func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTemporary || dnsErr.IsTimeout
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
			return nestedDNS.IsTemporary || nestedDNS.IsTimeout
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	return false
}

func (m *Monitor) verbosef(format string, args ...any) {
	if m == nil || !m.cfg.Verbose || m.logger == nil {
		return
	}
	m.logger.Printf("verbose "+format, args...)
}

func responseRequestID(headers http.Header) string {
	for _, key := range []string{"X-Request-Id", "X-Request-ID", "X-Aiko-Request-Id"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func maskedProjectKey(projectKey string) string {
	if len(projectKey) <= 10 {
		return "***"
	}
	return projectKey[:6] + "..." + projectKey[len(projectKey)-4:]
}

func resolveLogger(cfg Config) *log.Logger {
	if cfg.Logger != nil {
		return cfg.Logger
	}
	if cfg.Verbose {
		return log.New(os.Stderr, "[aiko] ", log.LstdFlags)
	}
	return log.New(io.Discard, "", 0)
}

func newMonitor(cfg Config, secret []byte, client *http.Client, logger *log.Logger) *Monitor {
	monitor := &Monitor{
		cfg:     cfg,
		secret:  secret,
		client:  client,
		logger:  logger,
		events:  make(chan Event, cfg.QueueSize),
		sem:     make(chan struct{}, cfg.MaxConcurrentSends),
		closeCh: make(chan struct{}),
		enabled: true,
		rnd:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	monitor.wg.Add(1)
	go monitor.run()
	return monitor
}

func newNoopMonitor(cfg Config) *Monitor {
	logger := resolveLogger(cfg)
	return &Monitor{
		cfg: Config{
			ProjectKey:         cfg.ProjectKey,
			SecretKey:          cfg.SecretKey,
			Endpoint:           cfg.Endpoint,
			Enabled:            cfg.Enabled,
			Verbose:            cfg.Verbose,
			Actor:              cfg.Actor,
			MaxConcurrentSends: cfg.MaxConcurrentSends,
			QueueSize:          cfg.QueueSize,
			HTTPClient:         cfg.HTTPClient,
			Logger:             logger,
		},
		client:  cfg.HTTPClient,
		logger:  logger,
		closeCh: make(chan struct{}),
		enabled: false,
	}
}

func initMonitor(cfg Config) (*Monitor, error) {
	logger := resolveLogger(cfg)
	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}
	if !enabled {
		cfg.Logger = logger
		cfg.Actor = normalizeActorConfig(cfg.Actor)
		if cfg.Verbose {
			logger.Printf("verbose init disabled")
		}
		return newNoopMonitor(cfg), nil
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}

	if err := ValidateConfig(cfg.ProjectKey, cfg.SecretKey, endpoint); err != nil {
		return nil, err
	}
	if err := validateActorConfig(cfg.Actor); err != nil {
		return nil, err
	}
	actor := normalizeActorConfig(cfg.Actor)

	secret, err := base64.RawURLEncoding.DecodeString(cfg.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("decode secret key: %w", err)
	}

	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}

	maxConcurrent := cfg.MaxConcurrentSends
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentSends
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	normalized := Config{
		ProjectKey:         cfg.ProjectKey,
		SecretKey:          cfg.SecretKey,
		Endpoint:           endpoint,
		Enabled:            cfg.Enabled,
		Verbose:            cfg.Verbose,
		Actor:              actor,
		MaxConcurrentSends: maxConcurrent,
		QueueSize:          queueSize,
		HTTPClient:         client,
		Logger:             logger,
	}

	monitor := newMonitor(normalized, secret, client, logger)
	monitor.verbosef(
		"init sdk=%s endpoint=%s project_key=%s queue_size=%d max_concurrent_sends=%d",
		VersionHeaderValue(),
		endpoint,
		maskedProjectKey(cfg.ProjectKey),
		queueSize,
		maxConcurrent,
	)
	return monitor, nil
}
