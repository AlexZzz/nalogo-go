package nalogo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Client is the main facade for the FNS "Мой налог" API.
// Construct with New; all I/O methods require a context.Context first arg.
type Client struct {
	cfg        *config
	apiClient  *http.Client // goes through authTransport (401 refresh)
	authClient *http.Client // plain — used for auth endpoints to avoid refresh loops
	inn        string       // populated after successful authentication
}

// New constructs a Client with the provided options.
func New(opts ...Option) *Client {
	cfg := newConfig()
	for _, o := range opts {
		o(cfg)
	}

	// Resolve the base transport: use the provided httpClient's transport (for
	// test injection via WithHTTPClient), or fall back to http.DefaultTransport.
	// authTransport always wraps the base — WithHTTPClient never bypasses it.
	baseTrans := http.RoundTripper(http.DefaultTransport)
	if cfg.httpClient != nil && cfg.httpClient.Transport != nil {
		baseTrans = cfg.httpClient.Transport
	}

	authClient := &http.Client{
		Transport: baseTrans,
		Timeout:   cfg.timeout,
	}

	at := &authTransport{
		base:       baseTrans,
		store:      cfg.store,
		authClient: authClient,
		baseURL:    cfg.baseURL,
		deviceID:   cfg.deviceID,
	}

	apiClient := &http.Client{
		Transport: at,
		Timeout:   cfg.timeout,
	}

	return &Client{
		cfg:        cfg,
		apiClient:  apiClient,
		authClient: authClient,
	}
}

// Income returns an Income API accessor.
func (c *Client) Income() *Income { return &Income{c: c} }

// Receipt returns a Receipt API accessor.
func (c *Client) Receipt() *Receipt { return &Receipt{c: c} }

// Tax returns a Tax API accessor.
func (c *Client) Tax() *Tax { return &Tax{c: c} }

// User returns a User API accessor.
func (c *Client) User() *User { return &User{c: c} }

// PaymentType returns a PaymentType API accessor.
func (c *Client) PaymentType() *PaymentType { return &PaymentType{c: c} }

// do is the internal request helper.
// It builds, sends, checks for errors, and decodes the JSON response into out.
// Pass out=nil to discard the response body (e.g. for void endpoints).
func (c *Client) do(ctx context.Context, client *http.Client, method, url string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	setDefaultHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// url1 builds a /v1/... API URL.
func (c *Client) url1(path string) string { return c.cfg.baseURL + "/v1/" + path }

// url2 builds a /v2/... API URL.
func (c *Client) url2(path string) string { return c.cfg.baseURL + "/v2/" + path }

// urlReceipt builds a receipt-specific URL (no /v1/ prefix per FNS API spec).
func (c *Client) urlReceipt(path string) string { return c.cfg.baseURL + "/" + path }

// INN returns the INN of the authenticated user (empty before authentication).
func (c *Client) INN() string { return c.inn }

// ensure the time package is used (via options.go — shared package, noop import guard).
var _ = time.Second
