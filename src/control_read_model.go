package yarouter

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	configschema "github.com/duvu/ya-router/internal/config"
	controlpkg "github.com/duvu/ya-router/internal/control"
	operationpkg "github.com/duvu/ya-router/internal/operation"
	providerpkg "github.com/duvu/ya-router/internal/provider"
	routingpkg "github.com/duvu/ya-router/internal/routing"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"
	secretpkg "github.com/duvu/ya-router/internal/secret"
)

type controlReadModel struct {
	runtime    *runtimepkg.Manager
	providers  *providerpkg.Manager
	operations *operationpkg.Manager
	secrets    secretpkg.SecretStore
	catalogs   *controlCatalogStore
}

type controlCatalogStore struct {
	mu      sync.RWMutex
	records map[providerpkg.ID]controlCatalogRecord
	now     func() time.Time
}

type controlCatalogRecord struct {
	models        []controlpkg.ModelResource
	fetchedAt     time.Time
	lastAttemptAt time.Time
	lastError     string
}

func newControlReadModel(runtimeManager *runtimepkg.Manager, providerManager *providerpkg.Manager, operationManager *operationpkg.Manager, secretStores ...secretpkg.SecretStore) *controlReadModel {
	model := &controlReadModel{
		runtime:    runtimeManager,
		providers:  providerManager,
		operations: operationManager,
		catalogs: &controlCatalogStore{
			records: make(map[providerpkg.ID]controlCatalogRecord),
			now:     time.Now,
		},
	}
	if len(secretStores) > 0 {
		model.secrets = secretStores[0]
	}
	return model
}

func (model *controlReadModel) Providers(_ context.Context) ([]controlpkg.ProviderResource, error) {
	if model == nil || model.providers == nil {
		return nil, fmt.Errorf("provider manager is unavailable")
	}
	statuses := model.providers.List()
	resources := make([]controlpkg.ProviderResource, 0, len(statuses))
	for _, status := range statuses {
		health := status.Health
		if health.Health.LastError != "" {
			health.Health.LastError = "provider_error"
		}
		resources = append(resources, controlpkg.ProviderResource{
			Descriptor:            status.Descriptor,
			Enabled:               status.Enabled,
			EffectiveCapabilities: append([]providerpkg.Capability(nil), status.EffectiveCapabilities...),
			Health:                health,
			Generation:            status.Generation,
		})
	}
	return resources, nil
}

func (model *controlReadModel) RoutingStatus(_ context.Context, publicID string) (controlpkg.RoutingStatusResource, bool, error) {
	if model == nil || model.runtime == nil {
		return controlpkg.RoutingStatusResource{}, false, fmt.Errorf("runtime manager is unavailable")
	}
	lease, err := model.runtime.Acquire()
	if err != nil {
		return controlpkg.RoutingStatusResource{}, false, err
	}
	defer lease.Release()
	router := lease.Snapshot().Router()
	if router == nil {
		return controlpkg.RoutingStatusResource{}, false, fmt.Errorf("router is unavailable")
	}
	snapshot := router.AvailabilitySnapshot()
	resource := controlpkg.RoutingStatusResource{
		PublicID:     publicID,
		Generation:   snapshot.Generation(),
		Capabilities: make([]routingpkg.VirtualModelReadiness, 0, 3),
		Counters:     router.Metrics().Snapshot(),
	}
	if manager := currentConfigState(); manager != nil {
		resource.ConfigRevision = manager.Snapshot().Revision
	}
	for _, capability := range []providerpkg.Capability{CapabilityChat, CapabilityResponses, CapabilityEmbeddings} {
		for _, readiness := range router.VirtualModelReadinessFor(capability, snapshot) {
			if readiness.VirtualModel == publicID {
				resource.Capabilities = append(resource.Capabilities, readiness)
			}
		}
	}
	return resource, len(resource.Capabilities) > 0, nil
}

func (model *controlReadModel) Accounts(_ context.Context) ([]controlpkg.AccountResource, error) {
	if model == nil || model.providers == nil {
		return nil, fmt.Errorf("provider manager is unavailable")
	}
	config, err := model.desiredConfig()
	if err != nil {
		return nil, err
	}
	statuses := model.providers.List()
	resources := make([]controlpkg.AccountResource, 0)
	for _, status := range statuses {
		for _, account := range status.Accounts {
			resources = append(resources, controlpkg.AccountResource{
				ProviderID: status.Descriptor.ID,
				ID:         account.ID,
				Label:      account.Label,
				Enabled:    account.Enabled,
				Priority:   account.Priority,
				Credential: credentialMetadata(config, status.Descriptor.ID, account.ID, account.Label, model.secrets),
			})
		}
	}
	return resources, nil
}

func (model *controlReadModel) Models(ctx context.Context, refresh bool) (controlpkg.ModelCatalogResponse, error) {
	if model == nil || model.runtime == nil || model.providers == nil || model.catalogs == nil {
		return controlpkg.ModelCatalogResponse{}, fmt.Errorf("model catalog dependencies are unavailable")
	}
	lease, err := model.runtime.Acquire()
	if err != nil {
		return controlpkg.ModelCatalogResponse{}, err
	}
	defer lease.Release()
	active := make(map[providerpkg.ID]providerpkg.Provider)
	for _, registered := range lease.Snapshot().Providers().All() {
		active[registered.ID()] = registered
	}
	statuses := model.providers.List()
	catalogs := make([]controlpkg.ProviderCatalog, 0, len(statuses))
	for _, status := range statuses {
		registered := active[status.Descriptor.ID]
		if status.Enabled && registered != nil {
			if refresh {
				if cache, ok := registered.(interface{ InvalidateModelCache() }); ok {
					cache.InvalidateModelCache()
				}
			}
			attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			list, listErr := registered.ListModels(attemptCtx)
			cancel()
			if listErr != nil {
				model.catalogs.markError(status.Descriptor.ID)
			} else {
				resources := make([]controlpkg.ModelResource, 0)
				if list != nil {
					for _, upstream := range list.Data {
						resources = append(resources, controlpkg.ModelResource{
							ID:         routingpkg.AddModelPrefix(status.Descriptor.ID, upstream.ID),
							UpstreamID: upstream.ID,
							ProviderID: status.Descriptor.ID,
							Available:  true,
							OwnedBy:    routingpkg.ProviderOwnedBy(status.Descriptor.ID),
							Endpoints:  append([]string(nil), upstream.SupportedEndpoints...),
						})
					}
				}
				model.catalogs.store(status.Descriptor.ID, resources)
			}
		}
		catalogs = append(catalogs, model.catalogs.resource(status))
	}
	return controlpkg.ModelCatalogResponse{Catalogs: catalogs}, nil
}

func (model *controlReadModel) Configuration(_ context.Context) (controlpkg.ConfigResource, error) {
	manager := currentConfigState()
	if manager == nil {
		return controlpkg.ConfigResource{}, fmt.Errorf("managed configuration state is unavailable")
	}
	snapshot := manager.Snapshot()
	return controlpkg.ConfigResource{
		Revision:        snapshot.Revision,
		Digest:          snapshot.Digest,
		EffectiveDigest: snapshot.EffectiveDigest,
		RestartRequired: append([]string(nil), snapshot.RestartRequired...),
		Desired:         configschema.Redact(snapshot.Desired),
		Effective:       configschema.Redact(snapshot.Effective),
	}, nil
}

func (model *controlReadModel) Operations(ctx context.Context) ([]controlpkg.OperationResource, error) {
	if model == nil || model.operations == nil {
		return nil, fmt.Errorf("operation manager is unavailable")
	}
	identity, _ := controlpkg.IdentityFromContext(ctx)
	all := model.operations.List()
	resources := make([]controlpkg.OperationResource, 0, len(all))
	for _, record := range all {
		if identity.Role == controlpkg.RoleAdmin || identity.Subject == record.Owner {
			resources = append(resources, record)
		}
	}
	return resources, nil
}

func (model *controlReadModel) Events(after uint64) []providerpkg.LifecycleEvent {
	if model == nil || model.providers == nil {
		return nil
	}
	return model.providers.Events().History(after)
}

func (model *controlReadModel) SubscribeEvents(buffer int) (<-chan providerpkg.LifecycleEvent, func()) {
	if model == nil || model.providers == nil {
		stream := make(chan providerpkg.LifecycleEvent)
		close(stream)
		return stream, func() {}
	}
	return model.providers.Events().Subscribe(buffer)
}

func (model *controlReadModel) desiredConfig() (*Config, error) {
	manager := currentConfigState()
	if manager == nil {
		return nil, fmt.Errorf("managed configuration state is unavailable")
	}
	return configschema.Clone(manager.Snapshot().Desired), nil
}

func (store *controlCatalogStore) store(id providerpkg.ID, models []controlpkg.ModelResource) {
	now := store.now().UTC()
	store.mu.Lock()
	store.records[id] = controlCatalogRecord{
		models:        cloneControlModels(models),
		fetchedAt:     now,
		lastAttemptAt: now,
	}
	store.mu.Unlock()
}

func (store *controlCatalogStore) markError(id providerpkg.ID) {
	now := store.now().UTC()
	store.mu.Lock()
	record := store.records[id]
	record.lastAttemptAt = now
	record.lastError = "catalog_refresh_failed"
	store.records[id] = record
	store.mu.Unlock()
}

func (store *controlCatalogStore) resource(status providerpkg.ProviderStatus) controlpkg.ProviderCatalog {
	store.mu.RLock()
	record := store.records[status.Descriptor.ID]
	store.mu.RUnlock()
	available := status.Enabled && status.Health.State != providerpkg.StateError && status.Health.State != providerpkg.StateDisabled
	models := cloneControlModels(record.models)
	for index := range models {
		models[index].Available = available
	}
	age := int64(0)
	if !record.fetchedAt.IsZero() {
		age = int64(store.now().Sub(record.fetchedAt).Seconds())
		if age < 0 {
			age = 0
		}
	}
	return controlpkg.ProviderCatalog{
		ProviderID:       status.Descriptor.ID,
		Enabled:          status.Enabled,
		Available:        available,
		Models:           models,
		FetchedAt:        record.fetchedAt,
		LastAttemptAt:    record.lastAttemptAt,
		AgeSeconds:       age,
		Stale:            record.lastError != "" || (!record.fetchedAt.IsZero() && age > int64(defaultModelCacheTTL.Seconds())),
		LastRefreshError: record.lastError,
	}
}

func cloneControlModels(source []controlpkg.ModelResource) []controlpkg.ModelResource {
	if source == nil {
		return []controlpkg.ModelResource{}
	}
	cloned := append([]controlpkg.ModelResource(nil), source...)
	for index := range cloned {
		cloned[index].Endpoints = append([]string(nil), cloned[index].Endpoints...)
	}
	return cloned
}

func credentialMetadata(config *Config, providerID providerpkg.ID, accountID, label string, store secretpkg.SecretStore) controlpkg.CredentialMetadata {
	switch providerID {
	case providerpkg.Copilot:
		auth := config.Providers.Copilot.Auth
		for _, account := range config.Providers.Copilot.Accounts {
			if sameAccount(accountID, label, account.ID, account.Label) {
				auth = account.Auth
				break
			}
		}
		if credential, ok := storedCredentialMetadata(store, "copilot/token"); ok {
			_, refreshable := storedCredentialMetadata(store, "copilot/github_token")
			credential.Refreshable = refreshable
			return credential
		}
		configured := auth.GitHubToken != "" || auth.CopilotToken != "" || auth.GitHubTokenRef != nil || auth.CopilotTokenRef != nil
		return controlpkg.CredentialMetadata{Configured: configured, Source: credentialSource(configured, auth.GitHubTokenRef, auth.CopilotTokenRef), Refreshable: auth.GitHubToken != "" || auth.GitHubTokenRef != nil}
	case providerpkg.Codex:
		auth := config.Providers.Codex.Auth
		for _, account := range config.Providers.Codex.Accounts {
			if sameAccount(accountID, label, account.ID, account.Label) {
				auth = account.Auth
				break
			}
		}
		if isAPIKeyMode(auth.Mode) {
			if credential, ok := storedCredentialMetadata(store, "codex/api_key"); ok {
				return credential
			}
		} else if credential, ok := storedCredentialMetadata(store, "codex/access_token"); ok {
			_, refreshable := storedCredentialMetadata(store, "codex/refresh_token")
			credential.Refreshable = refreshable
			return credential
		}
		configured := auth.APIKey != "" || auth.AccessToken != "" || auth.RefreshToken != "" || auth.APIKeyRef != nil || auth.AccessTokenRef != nil || auth.RefreshTokenRef != nil || strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
		source := credentialSource(configured, auth.APIKeyRef, auth.AccessTokenRef, auth.RefreshTokenRef)
		if source == "legacy_config" && auth.APIKey == "" && auth.AccessToken == "" && auth.RefreshToken == "" && strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
			source = "environment"
		}
		return controlpkg.CredentialMetadata{Configured: configured, Source: source, Refreshable: auth.RefreshToken != "" || auth.RefreshTokenRef != nil}
	case providerpkg.Kilo:
		if credential, ok := storedCredentialMetadata(store, "kilo/api_key"); ok {
			return credential
		}
		configured := config.Providers.Kilo.AllowAnonymous || config.Providers.Kilo.APIKey != "" || config.Providers.Kilo.APIKeyRef != nil || strings.TrimSpace(os.Getenv("KILO_API_KEY")) != ""
		source := "anonymous"
		if config.Providers.Kilo.APIKeyRef != nil {
			source = secretSource(config.Providers.Kilo.APIKeyRef)
		} else if config.Providers.Kilo.APIKey != "" {
			source = "legacy_config"
		} else if strings.TrimSpace(os.Getenv("KILO_API_KEY")) != "" {
			source = "environment"
		} else if !config.Providers.Kilo.AllowAnonymous {
			source = "unconfigured"
		}
		return controlpkg.CredentialMetadata{Configured: configured, Source: source}
	default:
		return controlpkg.CredentialMetadata{Source: "unconfigured"}
	}
}

func storedCredentialMetadata(store secretpkg.SecretStore, slot string) (controlpkg.CredentialMetadata, bool) {
	if store == nil {
		return controlpkg.CredentialMetadata{}, false
	}
	metadata, ok := store.Metadata(slot)
	if !ok || !metadata.Configured {
		return controlpkg.CredentialMetadata{}, false
	}
	return controlpkg.CredentialMetadata{Configured: true, Source: string(metadata.Source)}, true
}

func credentialSource(configured bool, references ...*SecretReference) string {
	for _, reference := range references {
		if reference != nil {
			return secretSource(reference)
		}
	}
	if configured {
		return "legacy_config"
	}
	return "unconfigured"
}

func secretSource(reference *SecretReference) string {
	if reference == nil {
		return "unconfigured"
	}
	if source := strings.TrimSpace(reference.Source); source != "" {
		return "secret_store:" + source
	}
	return "secret_store"
}
