package nalogo

import (
	"context"
	"net/http"
)

// UserResponse is the user profile response from GET /v1/user.
type UserResponse struct {
	ID          string `json:"id"`
	INN         string `json:"inn"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Status      string `json:"status"`
}

// User is the user API accessor.
type User struct{ c *Client }

// Get returns the current user's profile.
func (u *User) Get(ctx context.Context) (*UserResponse, error) {
	var resp UserResponse
	if err := u.c.do(ctx, u.c.apiClient, http.MethodGet, u.c.url1("user"), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
