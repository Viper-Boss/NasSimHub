package nodeclient

import (
	"context"
	"net/http"
)

// LocalAdminAccess is delivered through the pinned, owner-authenticated Node
// protocol. Never persist or log SetupCode; it is absent after initialization.
type LocalAdminAccess struct {
	Configured bool   `json:"configured"`
	SetupCode  string `json:"setup_code,omitempty"`
	Port       int    `json:"port"`
}

func (c *Client) LocalAdministration(ctx context.Context) (LocalAdminAccess, error) {
	var result LocalAdminAccess
	err := c.call(ctx, http.MethodPost, "/v1/admin/access", struct{}{}, &result)
	return result, err
}
