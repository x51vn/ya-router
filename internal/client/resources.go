package client

import (
	"context"
	"fmt"

	"github.com/duvu/ya-router/internal/control"
	"github.com/duvu/ya-router/internal/secret"
)

// Meta returns the daemon's control metadata and negotiated client
// compatibility. Callers should use CheckCompatible before any mutation.
func (c *Client) Meta(ctx context.Context) (control.MetaResponse, error) {
	var out control.MetaResponse
	err := c.doJSON(ctx, "GET", "/control/v1/meta", nil, &out)
	return out, err
}

// CheckCompatible verifies the client protocol version is inside the daemon's
// supported window. It returns a typed error (never a partial success) so
// unsupported combinations fail before any mutation is attempted.
func (c *Client) CheckCompatible(ctx context.Context) error {
	meta, err := c.Meta(ctx)
	if err != nil {
		return err
	}
	if !meta.Client.Compatible {
		return fmt.Errorf("client version %s is outside the daemon supported window [%s, %s]",
			ClientVersion, meta.Client.Minimum, meta.Client.Maximum)
	}
	return nil
}

// listEnvelope matches the {"data": [...]} shape used by list read routes.
type listEnvelope[T any] struct {
	Data []T `json:"data"`
}

// Providers returns the redacted provider resources.
func (c *Client) Providers(ctx context.Context) ([]control.ProviderResource, error) {
	var out listEnvelope[control.ProviderResource]
	if err := c.doJSON(ctx, "GET", "/control/v1/providers", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Accounts returns the redacted account resources.
func (c *Client) Accounts(ctx context.Context) ([]control.AccountResource, error) {
	var out listEnvelope[control.AccountResource]
	if err := c.doJSON(ctx, "GET", "/control/v1/accounts", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Models returns the model catalog. When refresh is true the daemon is asked to
// refresh provider catalogs (still bounded by daemon policy).
func (c *Client) Models(ctx context.Context, refresh bool) (control.ModelCatalogResponse, error) {
	path := "/control/v1/models"
	if refresh {
		path += "?refresh=true"
	}
	var out control.ModelCatalogResponse
	err := c.doJSON(ctx, "GET", path, nil, &out)
	return out, err
}

// Configuration returns the redacted, revisioned configuration resource.
func (c *Client) Configuration(ctx context.Context) (control.ConfigResource, error) {
	var out control.ConfigResource
	err := c.doJSON(ctx, "GET", "/control/v1/config", nil, &out)
	return out, err
}

func (c *Client) RoutingStatus(ctx context.Context) (control.RoutingStatusResource, error) {
	var out control.RoutingStatusResource
	err := c.doJSON(ctx, "GET", "/control/v1/routing/thiendu", nil, &out)
	return out, err
}

// Operations returns the recorded async operations.
func (c *Client) Operations(ctx context.Context) ([]control.OperationResource, error) {
	var out listEnvelope[control.OperationResource]
	if err := c.doJSON(ctx, "GET", "/control/v1/operations", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Operation returns one operation by ID.
func (c *Client) Operation(ctx context.Context, id string) (control.OperationResource, error) {
	var out control.OperationResource
	if id == "" {
		return out, fmt.Errorf("operation id is required")
	}
	err := c.doJSON(ctx, "GET", "/control/v1/operations/"+escapePath(id), nil, &out)
	return out, err
}

type AuthSessionRequest struct {
	ProviderID       string `json:"provider_id"`
	AccountID        string `json:"account_id,omitempty"`
	Method           string `json:"method"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty"`
}

func (c *Client) CreateAuthSession(ctx context.Context, request AuthSessionRequest) (control.OperationResource, error) {
	var out control.OperationResource
	err := c.doJSON(ctx, "POST", "/control/v1/auth-sessions", request, &out)
	return out, err
}

func (c *Client) CancelAuthSession(ctx context.Context, id string) (control.OperationResource, error) {
	var out control.OperationResource
	if id == "" {
		return out, fmt.Errorf("auth session id is required")
	}
	err := c.doJSON(ctx, "DELETE", "/control/v1/auth-sessions/"+escapePath(id), nil, &out)
	return out, err
}

// MutationResult mirrors the daemon's redacted mutation outcome.
type MutationResult struct {
	DryRun           bool     `json:"dry_run"`
	Applied          bool     `json:"applied"`
	RuntimePublished bool     `json:"runtime_published"`
	CurrentRevision  uint64   `json:"current_revision"`
	NextRevision     uint64   `json:"next_revision"`
	Changed          bool     `json:"changed"`
	ChangedPaths     []string `json:"changed_paths,omitempty"`
	RestartRequired  []string `json:"restart_required,omitempty"`
}

// ApplyMutation submits a revision-safe configuration mutation. The mutation is
// a POST and carries an explicit ExpectedRevision, so the daemon rejects a
// stale write with a typed conflict rather than overwriting a newer revision;
// this client never auto-retries a mutation.
func (c *Client) ApplyMutation(ctx context.Context, request control.MutationRequest) (MutationResult, error) {
	var out MutationResult
	err := c.doJSON(ctx, "POST", "/control/v1/config/mutations", request, &out)
	return out, err
}

// Secrets returns redacted secret metadata (never values). It reports each
// credential slot's source, whether it is configured, and whether it is
// read-only (environment/official-store provided).
func (c *Client) Secrets(ctx context.Context) ([]secret.Metadata, error) {
	var out listEnvelope[secret.Metadata]
	if err := c.doJSON(ctx, "GET", "/control/v1/secrets", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

type secretWriteRequest struct {
	Slot  string `json:"slot"`
	Value string `json:"value"`
}

func (c *Client) SetSecret(ctx context.Context, slot, value string) (secret.Metadata, error) {
	var out secret.Metadata
	err := c.doJSON(ctx, "PUT", "/control/v1/secrets", secretWriteRequest{Slot: slot, Value: value}, &out)
	return out, err
}

func (c *Client) DeleteSecret(ctx context.Context, slot string) error {
	return c.doJSON(ctx, "DELETE", "/control/v1/secrets", struct {
		Slot string `json:"slot"`
	}{Slot: slot}, nil)
}

// Events returns a bounded page of lifecycle events after the given cursor.
func (c *Client) Events(ctx context.Context, after uint64) (control.EventPage, error) {
	var out control.EventPage
	path := fmt.Sprintf("/control/v1/events?after=%d", after)
	err := c.doJSON(ctx, "GET", path, nil, &out)
	return out, err
}
