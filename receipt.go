package nalogo

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Receipt is the receipt API accessor.
type Receipt struct{ c *Client }

// PrintURL returns the print URL for a receipt without making an HTTP request.
// Requires the client to be authenticated (INN must be set via CreateAccessToken or Authenticate).
func (r *Receipt) PrintURL(receiptUUID string) (string, error) {
	receiptUUID = strings.TrimSpace(receiptUUID)
	if receiptUUID == "" {
		return "", newValidationError("receiptUUID cannot be empty")
	}
	inn := r.c.INN()
	if inn == "" {
		return "", &APIError{Sentinel: ErrNotAuthenticated, StatusCode: 0, Body: "call CreateAccessToken or Authenticate first"}
	}
	return r.c.urlReceipt(fmt.Sprintf("receipt/%s/%s/print", inn, receiptUUID)), nil
}

// JSON retrieves the full JSON data for a receipt.
func (r *Receipt) JSON(ctx context.Context, receiptUUID string) (map[string]any, error) {
	receiptUUID = strings.TrimSpace(receiptUUID)
	if receiptUUID == "" {
		return nil, newValidationError("receiptUUID cannot be empty")
	}
	inn := r.c.INN()
	if inn == "" {
		return nil, &APIError{Sentinel: ErrNotAuthenticated, StatusCode: 0, Body: "call CreateAccessToken or Authenticate first"}
	}

	var result map[string]any
	url := r.c.urlReceipt(fmt.Sprintf("receipt/%s/%s/json", inn, receiptUUID))
	if err := r.c.do(ctx, r.c.apiClient, http.MethodGet, url, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}
