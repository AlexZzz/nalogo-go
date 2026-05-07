package nalogo

import (
	"context"
	"net/http"
)

// PaymentTypeEntry is a single entry from GET /v1/payment-type/table.
type PaymentTypeEntry struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Favorite bool           `json:"favorite"`
	Extra    map[string]any `json:"-"`
}

// PaymentType is the payment-type API accessor.
type PaymentType struct{ c *Client }

// Table returns all available payment types.
func (p *PaymentType) Table(ctx context.Context) ([]PaymentTypeEntry, error) {
	var resp []PaymentTypeEntry
	if err := p.c.do(ctx, p.c.apiClient, http.MethodGet, p.c.url1("payment-type/table"), nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// Favorite returns the first payment type marked as favorite, or nil if none.
func (p *PaymentType) Favorite(ctx context.Context) (*PaymentTypeEntry, error) {
	entries, err := p.Table(ctx)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Favorite {
			return &entries[i], nil
		}
	}
	return nil, nil
}
