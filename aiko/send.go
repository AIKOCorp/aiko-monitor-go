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
	"sync"
	"syscall"
	"time"
)

type Monitor struct {
	cfg     Config
	secret  []byte
	client  *http.Client
	logger  *log.Logger
	events  chan Event
	sem     chan struct{}
	wg      sync.WaitGroup
	once    sync.Once
	closeCh chan struct{}
	enabled bool
	rnd     *rand.Rand
}

const (
	maxAttempts    = 3
	baseBackoff    = 250 * time.Millisecond
	maxBackoff     = 2 * time.Second
	requestTimeout = 10 * time.Second
)

func (m *Monitor) AddEvent(evt Event) {
	if m == nil || !m.enabled {
		return
	}
	select {
	case m.events <- evt:
	case <-m.closeCh:
	default:
		m.logger.Printf("aiko monitor queue is full; dropping event")
	} // shouldn't drop
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
	factor := 0.8 + 0.4*m.rnd.Float64()
	return time.Duration(float64(base) * factor)
}

func (m *Monitor) send(evt Event) {
	sanitized := RedactEvent(evt)
	payload, err := GzipEvent(sanitized)
	if err != nil {
		return
	}

	signature := Sign(m.secret, payload)
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

		time.Sleep(m.jitter(backoff))
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
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Monitor{
		cfg: Config{
			ProjectKey:         cfg.ProjectKey,
			SecretKey:          cfg.SecretKey,
			Endpoint:           cfg.Endpoint,
			Enabled:            cfg.Enabled,
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
	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}
	if !enabled {
		return newNoopMonitor(cfg), nil
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}

	if err := ValidateConfig(cfg.ProjectKey, cfg.SecretKey, endpoint); err != nil {
		return nil, err
	}

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

	logger := cfg.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	normalized := Config{
		ProjectKey:         cfg.ProjectKey,
		SecretKey:          cfg.SecretKey,
		Endpoint:           endpoint,
		Enabled:            cfg.Enabled,
		MaxConcurrentSends: maxConcurrent,
		QueueSize:          queueSize,
		HTTPClient:         client,
		Logger:             logger,
	}

	return newMonitor(normalized, secret, client, logger), nil
}
