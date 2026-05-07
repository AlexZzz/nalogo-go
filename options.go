package nalogo

import (
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultBaseURL   = "https://lknpd.nalog.ru/api"
	defaultTimeout   = 10 * time.Second
	defaultAppVer    = "1.0.0"
	defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 11_2_2) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/88.0.4324.192 Safari/537.36"
)

type config struct {
	baseURL    string
	timeout    time.Duration
	deviceID   string
	store      TokenStore
	httpClient *http.Client // if set, bypasses authTransport construction
	logger     *slog.Logger
}

func newConfig() *config {
	return &config{
		baseURL:  defaultBaseURL,
		timeout:  defaultTimeout,
		deviceID: generateDeviceID(),
		store:    &MemoryStore{},
		logger:   slog.Default(),
	}
}

// Option mutates the client configuration.
type Option func(*config)

// WithBaseURL overrides the FNS API base URL (default: https://lknpd.nalog.ru/api).
func WithBaseURL(u string) Option {
	return func(c *config) { c.baseURL = u }
}

// WithTimeout sets the HTTP client timeout (default: 10s).
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithDeviceID sets the device ID sent in every auth request.
func WithDeviceID(id string) Option {
	return func(c *config) { c.deviceID = id }
}

// WithTokenStore plugs in a custom TokenStore (default: MemoryStore).
func WithTokenStore(s TokenStore) Option {
	return func(c *config) { c.store = s }
}

// WithHTTPClient bypasses internal transport construction and uses the
// provided client for all API requests. Intended for testing.
func WithHTTPClient(cl *http.Client) Option {
	return func(c *config) { c.httpClient = cl }
}

// WithLogger sets the structured logger (default: slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(c *config) { c.logger = l }
}
