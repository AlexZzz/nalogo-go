package nalogo

import (
	"context"
	"encoding/json"
	"net/http"
)

// ChallengeResponse is returned by CreatePhoneChallenge.
type ChallengeResponse struct {
	ChallengeToken string `json:"challengeToken"`
	ExpireDate     string `json:"expireDate"`
	ExpireIn       int    `json:"expireIn"`
}

// authRequest is the payload for INN+password login.
type authRequest struct {
	Username   string         `json:"username"`
	Password   string         `json:"password"`
	DeviceInfo map[string]any `json:"deviceInfo"`
}

// phoneStartRequest is the payload for SMS challenge start (v2).
type phoneStartRequest struct {
	Phone                string `json:"phone"`
	RequireTpToBeActive  bool   `json:"requireTpToBeActive"`
}

// phoneVerifyRequest is the payload for SMS challenge verify.
type phoneVerifyRequest struct {
	Phone          string         `json:"phone"`
	Code           string         `json:"code"`
	ChallengeToken string         `json:"challengeToken"`
	DeviceInfo     map[string]any `json:"deviceInfo"`
}

// CreateAccessToken authenticates via INN + password.
// Returns the raw token JSON string (mirrors upstream).
// Persists the token to the configured TokenStore.
func (c *Client) CreateAccessToken(ctx context.Context, inn, password string) (string, error) {
	payload := authRequest{
		Username:   inn,
		Password:   password,
		DeviceInfo: buildDeviceInfo(c.cfg.deviceID),
	}

	var raw json.RawMessage
	if err := c.do(ctx, c.authClient, http.MethodPost, c.url1("auth/lkfl"), payload, &raw); err != nil {
		return "", err
	}

	tokenJSON := string(raw)

	var td TokenData
	if err := json.Unmarshal(raw, &td); err != nil {
		return "", err
	}
	if err := c.cfg.store.Save(ctx, &td); err != nil {
		return "", err
	}
	c.setINN(td.Profile.INN)

	return tokenJSON, nil
}

// CreatePhoneChallenge starts the two-step SMS authentication (v2 endpoint).
func (c *Client) CreatePhoneChallenge(ctx context.Context, phone string) (*ChallengeResponse, error) {
	payload := phoneStartRequest{
		Phone:               phone,
		RequireTpToBeActive: true,
	}

	var resp ChallengeResponse
	if err := c.do(ctx, c.authClient, http.MethodPost, c.url2("auth/challenge/sms/start"), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateAccessTokenByPhone completes SMS authentication.
// Returns the raw token JSON string. Persists the token to the configured TokenStore.
func (c *Client) CreateAccessTokenByPhone(ctx context.Context, phone, challengeToken, code string) (string, error) {
	payload := phoneVerifyRequest{
		Phone:          phone,
		Code:           code,
		ChallengeToken: challengeToken,
		DeviceInfo:     buildDeviceInfo(c.cfg.deviceID),
	}

	var raw json.RawMessage
	if err := c.do(ctx, c.authClient, http.MethodPost, c.url1("auth/challenge/sms/verify"), payload, &raw); err != nil {
		return "", err
	}

	tokenJSON := string(raw)

	var td TokenData
	if err := json.Unmarshal(raw, &td); err != nil {
		return "", err
	}
	if err := c.cfg.store.Save(ctx, &td); err != nil {
		return "", err
	}
	c.setINN(td.Profile.INN)

	return tokenJSON, nil
}

// Authenticate loads a previously obtained token JSON into the client.
// After this call, all API requests will use the token.
func (c *Client) Authenticate(ctx context.Context, tokenJSON string) error {
	var td TokenData
	if err := json.Unmarshal([]byte(tokenJSON), &td); err != nil {
		return err
	}
	if err := c.cfg.store.Save(ctx, &td); err != nil {
		return err
	}
	c.setINN(td.Profile.INN)
	return nil
}
