package control

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	configschema "github.com/duvu/ya-router/internal/config"
	operationpkg "github.com/duvu/ya-router/internal/operation"
	providerpkg "github.com/duvu/ya-router/internal/provider"
)

// CredentialMetadata is the redacted credential posture for one daemon-owned
// account. It never contains token material or upstream account identifiers.
type CredentialMetadata struct {
	Configured  bool   `json:"configured"`
	Source      string `json:"source"`
	Refreshable bool   `json:"refreshable"`
}

type ProviderResource struct {
	Descriptor            providerpkg.Descriptor   `json:"descriptor"`
	Enabled               bool                     `json:"enabled"`
	EffectiveCapabilities []providerpkg.Capability `json:"effective_capabilities"`
	Health                providerpkg.HealthRecord `json:"health"`
	Generation            uint64                   `json:"generation"`
}

type AccountResource struct {
	ProviderID providerpkg.ID     `json:"provider_id"`
	ID         string             `json:"id"`
	Label      string             `json:"label,omitempty"`
	Enabled    bool               `json:"enabled"`
	Priority   int                `json:"priority"`
	Credential CredentialMetadata `json:"credential"`
}

type ModelResource struct {
	ID         string         `json:"id"`
	UpstreamID string         `json:"upstream_id"`
	ProviderID providerpkg.ID `json:"provider_id"`
	Available  bool           `json:"available"`
	OwnedBy    string         `json:"owned_by,omitempty"`
	Endpoints  []string       `json:"supported_endpoints,omitempty"`
}

type ProviderCatalog struct {
	ProviderID       providerpkg.ID  `json:"provider_id"`
	Enabled          bool            `json:"enabled"`
	Available        bool            `json:"available"`
	Models           []ModelResource `json:"models"`
	FetchedAt        time.Time       `json:"fetched_at,omitempty"`
	LastAttemptAt    time.Time       `json:"last_attempt_at,omitempty"`
	AgeSeconds       int64           `json:"age_seconds,omitempty"`
	Stale            bool            `json:"stale"`
	LastRefreshError string          `json:"last_refresh_error,omitempty"`
}

type ModelCatalogResponse struct {
	Catalogs []ProviderCatalog `json:"catalogs"`
}

type ConfigResource struct {
	Revision        uint64               `json:"revision"`
	Digest          string               `json:"digest"`
	EffectiveDigest string               `json:"effective_digest"`
	RestartRequired []string             `json:"restart_required,omitempty"`
	Desired         *configschema.Config `json:"desired"`
	Effective       *configschema.Config `json:"effective"`
}

type OperationResource = operationpkg.Record

type EventPage struct {
	Data      []providerpkg.LifecycleEvent `json:"data"`
	NextAfter uint64                       `json:"next_after"`
}

type ReadModel interface {
	Providers(context.Context) ([]ProviderResource, error)
	Accounts(context.Context) ([]AccountResource, error)
	Models(context.Context, bool) (ModelCatalogResponse, error)
	Configuration(context.Context) (ConfigResource, error)
	Operations(context.Context) ([]OperationResource, error)
	Events(uint64) []providerpkg.LifecycleEvent
	SubscribeEvents(int) (<-chan providerpkg.LifecycleEvent, func())
}

// RegisterReadRoutes adds the complete redacted read model without mutation
// endpoints. All resources require at least the viewer role.
func RegisterReadRoutes(api *API, model ReadModel) {
	api.Handle(http.MethodGet, "/control/v1/providers", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resources, err := model.Providers(request.Context())
		if err != nil {
			writeError(writer, request, http.StatusServiceUnavailable, "provider_read_failed", "Provider state is temporarily unavailable.", true, nil)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": nonNilProviders(resources)})
	}))
	api.Handle(http.MethodGet, "/control/v1/accounts", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resources, err := model.Accounts(request.Context())
		if err != nil {
			writeError(writer, request, http.StatusServiceUnavailable, "account_read_failed", "Account state is temporarily unavailable.", true, nil)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": nonNilAccounts(resources)})
	}))
	api.Handle(http.MethodGet, "/control/v1/models", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resources, err := model.Models(request.Context(), queryBool(request, "refresh"))
		if err != nil {
			writeError(writer, request, http.StatusServiceUnavailable, "model_catalog_read_failed", "Model catalog state is temporarily unavailable.", true, nil)
			return
		}
		if resources.Catalogs == nil {
			resources.Catalogs = []ProviderCatalog{}
		}
		writeJSON(writer, http.StatusOK, resources)
	}))
	api.Handle(http.MethodGet, "/control/v1/config", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resource, err := model.Configuration(request.Context())
		if err != nil {
			writeError(writer, request, http.StatusServiceUnavailable, "config_read_failed", "Configuration state is temporarily unavailable.", true, nil)
			return
		}
		writeJSON(writer, http.StatusOK, resource)
	}))
	api.Handle(http.MethodGet, "/control/v1/operations", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resources, err := model.Operations(request.Context())
		if err != nil {
			writeError(writer, request, http.StatusServiceUnavailable, "operation_read_failed", "Operation state is temporarily unavailable.", true, nil)
			return
		}
		if resources == nil {
			resources = []OperationResource{}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": resources})
	}))
	api.Handle(http.MethodGet, "/control/v1/events", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		after, err := eventCursor(request)
		if err != nil {
			writeError(writer, request, http.StatusBadRequest, "invalid_event_cursor", "The event cursor must be an unsigned integer.", false, nil)
			return
		}
		events := model.Events(after)
		writeJSON(writer, http.StatusOK, EventPage{Data: nonNilEvents(events), NextAfter: lastSequence(after, events)})
	}))
	api.Handle(http.MethodGet, "/control/v1/events/stream", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serveEventStream(writer, request, model)
	}))
}

func serveEventStream(writer http.ResponseWriter, request *http.Request, model ReadModel) {
	after, err := eventCursor(request)
	if err != nil {
		writeError(writer, request, http.StatusBadRequest, "invalid_event_cursor", "Last-Event-ID must be an unsigned integer.", false, nil)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, request, http.StatusInternalServerError, "streaming_not_supported", "The HTTP transport does not support event streaming.", false, nil)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)

	// Subscribe before reading history so an event published during replay is
	// either present in history or buffered in the live stream. Sequence
	// de-duplication below makes overlap harmless.
	stream, cancel := model.SubscribeEvents(32)
	defer cancel()
	for _, event := range model.Events(after) {
		if event.Sequence <= after {
			continue
		}
		if err := writeSSEEvent(writer, event); err != nil {
			return
		}
		after = event.Sequence
	}
	flusher.Flush()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-stream:
			if !open {
				return
			}
			if event.Sequence <= after {
				continue
			}
			if err := writeSSEEvent(writer, event); err != nil {
				return
			}
			after = event.Sequence
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprint(writer, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(writer http.ResponseWriter, event providerpkg.LifecycleEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, payload)
	return err
}

func eventCursor(request *http.Request) (uint64, error) {
	value := strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(request.URL.Query().Get("after"))
	}
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func queryBool(request *http.Request, name string) bool {
	value := strings.TrimSpace(request.URL.Query().Get(name))
	parsed, _ := strconv.ParseBool(value)
	return parsed || value == "1" || strings.EqualFold(value, "yes") || strings.EqualFold(value, "on")
}

func lastSequence(after uint64, events []providerpkg.LifecycleEvent) uint64 {
	if len(events) == 0 {
		return after
	}
	return events[len(events)-1].Sequence
}

func nonNilProviders(values []ProviderResource) []ProviderResource {
	if values == nil {
		return []ProviderResource{}
	}
	return values
}
func nonNilAccounts(values []AccountResource) []AccountResource {
	if values == nil {
		return []AccountResource{}
	}
	return values
}
func nonNilEvents(values []providerpkg.LifecycleEvent) []providerpkg.LifecycleEvent {
	if values == nil {
		return []providerpkg.LifecycleEvent{}
	}
	return values
}

// ReadEventPage decodes the polling fallback and is shared by future clients.
func ReadEventPage(reader *bufio.Reader) (EventPage, error) {
	var page EventPage
	err := json.NewDecoder(reader).Decode(&page)
	return page, err
}
