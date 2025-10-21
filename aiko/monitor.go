package aiko

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"sync"
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

func New(cfg Config) (*Monitor, error) {
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

	if err := validateConfig(cfg.ProjectKey, cfg.SecretKey, endpoint); err != nil {
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

	monitor := &Monitor{
		cfg: Config{
			ProjectKey:         cfg.ProjectKey,
			SecretKey:          cfg.SecretKey,
			Endpoint:           endpoint,
			Enabled:            cfg.Enabled,
			MaxConcurrentSends: maxConcurrent,
			QueueSize:          queueSize,
			HTTPClient:         client,
			Logger:             logger,
		},
		secret:  secret,
		client:  client,
		logger:  logger,
		events:  make(chan Event, queueSize),
		sem:     make(chan struct{}, maxConcurrent),
		closeCh: make(chan struct{}),
		enabled: true,
		rnd:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	monitor.wg.Add(1)
	go monitor.run()

	return monitor, nil
}

func NewNoop() *Monitor {
	return newNoopMonitor(Config{})
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

func (m *Monitor) AddEvent(evt Event) {
	if m == nil || !m.enabled {
		return
	}
	select {
	case m.events <- evt:
	case <-m.closeCh:
	default:
		m.logger.Printf("aiko monitor queue is full; dropping event")
	}
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
