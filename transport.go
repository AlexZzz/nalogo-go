package nalogo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// authTransport is an http.RoundTripper that:
//   - injects a Bearer token from TokenStore on every request
//   - on 401, acquires a mutex, double-checks for a concurrent refresh,
//     posts to /v1/auth/token, saves the new token, releases the mutex,
//     then retries the original request exactly once
type authTransport struct {
	base       http.RoundTripper
	store      TokenStore
	mu         sync.Mutex
	authClient *http.Client // plain client — must NOT go through authTransport
	baseURL    string
	deviceID   string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone request so we can replay it after refresh.
	req, bodyBytes, err := cloneRequest(req)
	if err != nil {
		return nil, err
	}

	if err := t.injectBearer(req); err != nil {
		return nil, err
	}

	// Read stale token before the first attempt, for double-check in refreshToken.
	staleTD, _ := t.store.Load(req.Context())
	staleToken := ""
	if staleTD != nil {
		staleToken = staleTD.Token
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	resp.Body.Close()

	// 401 — attempt single-flight refresh.
	if err := t.refreshToken(req.Context(), staleToken); err != nil {
		return nil, err
	}

	// Replay original request with refreshed token.
	req2, err := rebuildRequest(req, bodyBytes)
	if err != nil {
		return nil, err
	}
	if err := t.injectBearer(req2); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req2)
}

func (t *authTransport) injectBearer(req *http.Request) error {
	td, err := t.store.Load(req.Context())
	if err != nil {
		return err
	}
	if td == nil {
		return &APIError{Sentinel: ErrNotAuthenticated, StatusCode: 0}
	}
	req.Header.Set("Authorization", "Bearer "+td.Token)
	return nil
}

// refreshToken acquires the mutex and double-checks whether another goroutine
// already refreshed by comparing against staleToken (the token seen before the 401).
// If the stored token changed, the refresh already happened — return nil.
// Otherwise calls POST /v1/auth/token. Any failure returns ErrUnauthorized
// (the caller's request originally got a 401; the caller shouldn't see a
// different status from an internal refresh attempt).
func (t *authTransport) refreshToken(ctx context.Context, staleToken string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Double-check: if the token has changed, another goroutine refreshed it.
	td, err := t.store.Load(ctx)
	if err != nil {
		return err
	}
	if td != nil && td.Token != staleToken {
		return nil
	}
	if td == nil || td.RefreshToken == "" {
		return &APIError{Sentinel: ErrUnauthorized, StatusCode: http.StatusUnauthorized}
	}

	payload := map[string]any{
		"refreshToken": td.RefreshToken,
		"deviceInfo":   buildDeviceInfo(t.deviceID),
	}
	b, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.baseURL+"/v1/auth/token", bytes.NewReader(b))
	if err != nil {
		return &APIError{Sentinel: ErrUnauthorized, StatusCode: http.StatusUnauthorized, Body: err.Error()}
	}
	setDefaultHeaders(req)

	resp, err := t.authClient.Do(req)
	if err != nil {
		return &APIError{Sentinel: ErrUnauthorized, StatusCode: http.StatusUnauthorized, Body: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Always surface as 401 — from caller's perspective, auth failed.
		return &APIError{Sentinel: ErrUnauthorized, StatusCode: http.StatusUnauthorized}
	}

	var newTD TokenData
	if err := json.NewDecoder(resp.Body).Decode(&newTD); err != nil {
		return &APIError{Sentinel: ErrUnauthorized, StatusCode: http.StatusUnauthorized}
	}
	return t.store.Save(ctx, &newTD)
}

// DeviceInfo wire shape as required by FNS API.
func buildDeviceInfo(deviceID string) map[string]any {
	return map[string]any{
		"sourceType":     "WEB",
		"sourceDeviceId": deviceID,
		"appVersion":     defaultAppVer,
		"metaDetails":    map[string]string{"userAgent": defaultUserAgent},
	}
}

// setDefaultHeaders mirrors upstream Python default_headers.
func setDefaultHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Referrer", "https://lknpd.nalog.ru/auth/login")
}

func cloneRequest(req *http.Request) (*http.Request, []byte, error) {
	var buf []byte
	if req.Body != nil {
		var err error
		buf, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(buf))
	}
	clone := req.Clone(req.Context())
	return clone, buf, nil
}

func rebuildRequest(orig *http.Request, body []byte) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(orig.Context(), orig.Method, orig.URL.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	for k, vs := range orig.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return req, nil
}
