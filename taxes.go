package nalogo

import (
	"context"
	"net/http"
)

// Tax is the tax API accessor.
type Tax struct{ c *Client }

// Get returns current tax information (GET /v1/taxes).
func (t *Tax) Get(ctx context.Context) (map[string]any, error) {
	var resp map[string]any
	if err := t.c.do(ctx, t.c.apiClient, http.MethodGet, t.c.url1("taxes"), nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// History returns tax history, optionally filtered by OKTMO code.
func (t *Tax) History(ctx context.Context, oktmo string) (map[string]any, error) {
	payload := map[string]any{"oktmo": oktmo}
	var resp map[string]any
	if err := t.c.do(ctx, t.c.apiClient, http.MethodPost, t.c.url1("taxes/history"), payload, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// Payments returns tax payment records, optionally filtered by OKTMO.
func (t *Tax) Payments(ctx context.Context, oktmo string, onlyPaid bool) (map[string]any, error) {
	payload := map[string]any{
		"oktmo":    oktmo,
		"onlyPaid": onlyPaid,
	}
	var resp map[string]any
	if err := t.c.do(ctx, t.c.apiClient, http.MethodPost, t.c.url1("taxes/payments"), payload, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}
