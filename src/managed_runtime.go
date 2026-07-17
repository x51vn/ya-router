package yarouter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	configschema "github.com/duvu/ya-router/internal/config"
	providerpkg "github.com/duvu/ya-router/internal/provider"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"
	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func newProviderManager(config *Config, runtimeManager *runtimepkg.Manager) (*providerpkg.Manager, error) {
	return newProviderManagerWithAuth(config, runtimeManager, nil)
}

func newProviderManagerWithAuth(config *Config, runtimeManager *runtimepkg.Manager, auth secretpkg.AuthController) (*providerpkg.Manager, error) {
	health := providerpkg.NewHealthRegistry()
	events := providerpkg.NewEventBus(256)
	manager, err := providerpkg.NewManager(runtimeManager, health, events, providerpkg.ManagerOptions{
		DrainTimeout: providerDrainTimeout(config),
		CloseTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	for _, factory := range compiledProviderFactoriesWithAuth(auth) {
		if err := manager.RegisterFactory(factory); err != nil {
			return nil, err
		}
	}
	if _, err := manager.Reconcile(context.Background(), desiredProviders(config)); err != nil {
		return nil, err
	}
	return manager, nil
}

func providerDrainTimeout(config *Config) time.Duration {
	seconds := config.Timeouts.ProxyContext
	if seconds <= 0 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

func compiledProviderFactories() []providerpkg.Factory {
	return compiledProviderFactoriesWithAuth(nil)
}

func compiledProviderFactoriesWithAuth(auth secretpkg.AuthController) []providerpkg.Factory {
	return []providerpkg.Factory{
		providerpkg.FactoryFuncs{
			ProviderDescriptor: providerpkg.Descriptor{
				ID:           ProviderCopilot,
				Name:         "GitHub Copilot",
				Capabilities: []Capability{CapabilityChat, CapabilityEmbeddings},
				AuthMethods: []providerpkg.AuthMethod{
					providerpkg.AuthDeviceCode,
					providerpkg.AuthManualTokenRecovery,
				},
				MultiAccount:  true,
				SchemaVersion: 1,
				ConfigSchema: []providerpkg.ConfigField{
					{Name: "allowed_models", Type: providerpkg.ConfigStrings},
					{Name: "account_cooldown_seconds", Type: providerpkg.ConfigInteger},
				},
			},
			ValidateConfigFunc: validateRuntimeConfig,
			BuildFunc: func(_ context.Context, value any) (Provider, error) {
				config := configschema.Clone(value.(*Config))
				return NewCopilotProviderWithAuth(config, auth), nil
			},
			ValidateFunc: validateBuiltProvider,
		},
		providerpkg.FactoryFuncs{
			ProviderDescriptor: providerpkg.Descriptor{
				ID:           ProviderCodex,
				Name:         "OpenAI Codex",
				Capabilities: []Capability{CapabilityChat, CapabilityResponses, CapabilityEmbeddings},
				AuthMethods: []providerpkg.AuthMethod{
					providerpkg.AuthDeviceCode,
					providerpkg.AuthAPIKey,
				},
				MultiAccount:  true,
				SchemaVersion: 1,
				ConfigSchema: []providerpkg.ConfigField{
					{Name: "api_key", Type: providerpkg.ConfigString, Secret: true},
					{Name: "allowed_models", Type: providerpkg.ConfigStrings},
					{Name: "account_cooldown_seconds", Type: providerpkg.ConfigInteger},
					{Name: "chatgpt_base_url", Type: providerpkg.ConfigString},
				},
			},
			ValidateConfigFunc: validateRuntimeConfig,
			BuildFunc: func(_ context.Context, value any) (Provider, error) {
				config := configschema.Clone(value.(*Config))
				return NewCodexProviderWithAuth(config, auth), nil
			},
			ValidateFunc: validateBuiltProvider,
		},
		providerpkg.FactoryFuncs{
			ProviderDescriptor: providerpkg.Descriptor{
				ID:            ProviderKilo,
				Name:          "Kilo Gateway",
				Capabilities:  []Capability{CapabilityChat, CapabilityResponses},
				AuthMethods:   []providerpkg.AuthMethod{providerpkg.AuthAnonymous, providerpkg.AuthAPIKey},
				MultiAccount:  false,
				SchemaVersion: 1,
				ConfigSchema: []providerpkg.ConfigField{
					{Name: "allow_anonymous", Type: providerpkg.ConfigBoolean},
					{Name: "api_key", Type: providerpkg.ConfigString, Secret: true},
					{Name: "organization_id", Type: providerpkg.ConfigString, Secret: true},
					{Name: "base_url", Type: providerpkg.ConfigString},
					{Name: "allowed_models", Type: providerpkg.ConfigStrings},
				},
			},
			ValidateConfigFunc: validateRuntimeConfig,
			BuildFunc: func(_ context.Context, value any) (Provider, error) {
				config := configschema.Clone(value.(*Config))
				registered := NewKiloProviderWithAuth(config, auth)
				if _, err := registered.baseURL(); err != nil {
					return nil, err
				}
				return registered, nil
			},
			ValidateFunc: validateBuiltProvider,
		},
	}
}

func validateRuntimeConfig(value any) error {
	config, ok := value.(*Config)
	if !ok || config == nil {
		return fmt.Errorf("runtime config has unexpected type")
	}
	return nil
}

func validateBuiltProvider(_ context.Context, registered providerpkg.Provider) error {
	if strings.TrimSpace(registered.Name()) == "" {
		return fmt.Errorf("provider name is required")
	}
	if len(registered.Capabilities()) == 0 {
		return fmt.Errorf("provider capabilities are required")
	}
	return nil
}

func desiredProviders(config *Config) []providerpkg.DesiredProvider {
	return []providerpkg.DesiredProvider{
		{
			ID:      ProviderCopilot,
			Enabled: config.Providers.Copilot.Enabled,
			Config:  configschema.Clone(config),
			ConfigFingerprint: runtimeConfigFingerprint(struct {
				Provider CopilotProviderConfig
				Timeouts TimeoutsConfig
			}{config.Providers.Copilot, config.Timeouts}),
			Accounts: copilotAccountState(config),
		},
		{
			ID:      ProviderCodex,
			Enabled: config.Providers.Codex.Enabled,
			Config:  configschema.Clone(config),
			ConfigFingerprint: runtimeConfigFingerprint(struct {
				Provider CodexProviderConfig
				Timeouts TimeoutsConfig
				ModelMap map[string]ModelMapEntry
			}{config.Providers.Codex, config.Timeouts, config.Routing.ModelMap}),
			Accounts: codexAccountState(config),
		},
		{
			ID:      ProviderKilo,
			Enabled: config.Providers.Kilo.Enabled,
			Config:  configschema.Clone(config),
			ConfigFingerprint: runtimeConfigFingerprint(struct {
				Provider KiloProviderConfig
				Timeouts TimeoutsConfig
			}{config.Providers.Kilo, config.Timeouts}),
			Accounts: []providerpkg.AccountState{{ID: "default", Label: "Default", Enabled: true}},
		},
	}
}

func runtimeConfigFingerprint(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func copilotAccountState(config *Config) []providerpkg.AccountState {
	if len(config.Providers.Copilot.Accounts) == 0 {
		return []providerpkg.AccountState{{ID: "default", Label: "Default", Enabled: true}}
	}
	accounts := make([]providerpkg.AccountState, 0, len(config.Providers.Copilot.Accounts))
	for index, account := range config.Providers.Copilot.Accounts {
		accounts = append(accounts, providerpkg.AccountState{
			ID:       account.ID,
			Label:    account.Label,
			Enabled:  true,
			Priority: index,
		})
	}
	return accounts
}

func codexAccountState(config *Config) []providerpkg.AccountState {
	if len(config.Providers.Codex.Accounts) == 0 {
		return []providerpkg.AccountState{{ID: "default", Label: "Default", Enabled: true}}
	}
	accounts := make([]providerpkg.AccountState, 0, len(config.Providers.Codex.Accounts))
	for index, account := range config.Providers.Codex.Accounts {
		accounts = append(accounts, providerpkg.AccountState{
			ID:       account.ID,
			Label:    account.Label,
			Enabled:  true,
			Priority: index,
		})
	}
	return accounts
}

func managedProxyHandler(manager *runtimepkg.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lease, err := manager.Acquire()
		if err != nil {
			writeOpenAIError(w, http.StatusServiceUnavailable, err)
			return
		}
		defer lease.Release()
		snapshot := lease.Snapshot()
		proxyHandler(snapshot.Providers(), snapshot.Router(), snapshot.Config()).ServeHTTP(w, r)
	}
}

func managedAnthropicHandler(manager *runtimepkg.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lease, err := manager.Acquire()
		if err != nil {
			writeAnthropicError(w, http.StatusServiceUnavailable, err)
			return
		}
		defer lease.Release()
		snapshot := lease.Snapshot()
		anthropicHandler(snapshot.Providers(), snapshot.Router(), snapshot.Config()).ServeHTTP(w, r)
	}
}

func managedModelsHandler(manager *runtimepkg.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lease, err := manager.Acquire()
		if err != nil {
			writeOpenAIError(w, http.StatusServiceUnavailable, err)
			return
		}
		defer lease.Release()
		snapshot := lease.Snapshot()
		modelsHandler(snapshot.Providers(), snapshot.Config()).ServeHTTP(w, r)
	}
}

func managedHealthHandler(providerManager *providerpkg.Manager, runtimeManager *runtimepkg.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		if path == "/health" {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"status":    "ok",
				"service":   "ya-router",
				"timestamp": time.Now().Unix(),
			})
			return
		}
		if path == "/health/umbrella" {
			writeManagedUmbrellaDiagnostics(w, runtimeManager)
			return
		}
		providerManager.RefreshHealth(r.Context())
		statuses := providerManager.List()
		enabled := make([]providerpkg.ProviderStatus, 0, len(statuses))
		for _, status := range statuses {
			if status.Enabled {
				enabled = append(enabled, status)
			}
		}
		switch path {
		case "/health/ready":
			writeManagedReadiness(w, enabled)
		case "/health/providers":
			writeManagedProviderHealth(w, enabled)
		default:
			writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
		}
	}
}

// writeManagedUmbrellaDiagnostics reports redacted virtual-model readiness for
// chat capability and bounded routing metrics. It performs no network I/O: the
// readiness view is computed from the network-free availability snapshot.
func writeManagedUmbrellaDiagnostics(w http.ResponseWriter, runtimeManager *runtimepkg.Manager) {
	lease, err := runtimeManager.Acquire()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"status": "unavailable"})
		return
	}
	defer lease.Release()
	router := lease.Snapshot().Router()
	snapshot := router.AvailabilitySnapshot()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "ok",
		"virtual_models": router.VirtualModelReadinessFor(CapabilityChat, snapshot),
		"metrics":        router.Metrics().Snapshot(),
	})
}

func writeManagedReadiness(w http.ResponseWriter, statuses []providerpkg.ProviderStatus) {
	if len(statuses) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status": "not_ready",
			"reason": "no providers registered",
		})
		return
	}
	ready := 0
	for _, status := range statuses {
		if status.Health.Health.Authenticated {
			ready++
		}
	}
	httpStatus := http.StatusOK
	state := "ready"
	if ready == 0 {
		httpStatus = http.StatusServiceUnavailable
		state = "not_ready"
	}
	writeJSON(w, httpStatus, map[string]interface{}{
		"status":          state,
		"ready_providers": ready,
		"total_providers": len(statuses),
	})
}

func writeManagedProviderHealth(w http.ResponseWriter, statuses []providerpkg.ProviderStatus) {
	providers := make([]map[string]interface{}, 0, len(statuses))
	for _, status := range statuses {
		providers = append(providers, map[string]interface{}{
			"id":            status.Descriptor.ID,
			"name":          status.Descriptor.Name,
			"authenticated": status.Health.Health.Authenticated,
			"can_refresh":   status.Health.Health.CanRefresh,
			"capabilities":  status.EffectiveCapabilities,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "providers": providers})
}
