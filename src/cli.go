// cli.go — command-line command handlers.
package yarouter

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	runtimepkg "github.com/duvu/ya-router/internal/runtime"
)

func printUsage() {
	fmt.Printf("ya-router\n\n")
	fmt.Printf("Usage: %s [command] [options]\n\n", os.Args[0])
	fmt.Printf("Commands:\n")
	fmt.Printf("  run|start             Start the proxy server\n")
	fmt.Printf("    --config-migrate    Config migration mode: merge (default), none, override\n")
	fmt.Printf("  auth [copilot|codex|kilo] Authenticate or enable a provider (default: copilot)\n")
	fmt.Printf("    --mode              Auth mode: device_code (default)\n")
	fmt.Printf("    --account <label>   Account label for multi-account pool\n")
	fmt.Printf("    --api-key-stdin     Read a Codex or Kilo API key from stdin\n")
	fmt.Printf("    --token-stdin       Read a ChatGPT manual token from stdin (recovery only)\n")
	fmt.Printf("  status                Show authentication status for all providers\n")
	fmt.Printf("  config                Show current configuration\n")
	fmt.Printf("  models [--provider P] [--refresh] List models\n")
	fmt.Printf("  refresh [--provider P] Force token refresh\n")
	fmt.Printf("  migrate-config        Migrate configuration file\n")
	fmt.Printf("  version               Show version information\n")
	fmt.Printf("  help                  Show this help\n\n")
	fmt.Printf("Server environment:\n")
	fmt.Printf("  YA_ROUTER_LISTEN_ADDRESS         Defaults to 127.0.0.1\n")
	fmt.Printf("  YA_ROUTER_API_KEY                Required for non-loopback binding\n")
	fmt.Printf("  YA_ROUTER_CORS_ALLOWED_ORIGINS   Comma-separated origin allowlist\n\n")
	fmt.Printf("Kilo environment:\n")
	fmt.Printf("  KILO_API_KEY                     Optional; free models support anonymous access\n")
	fmt.Printf("  KILO_ORG_ID                      Optional organization context\n")
	fmt.Printf("  KILO_GATEWAY_BASE_URL            Defaults to https://api.kilo.ai/api/gateway\n\n")
	flag.PrintDefaults()
}

func handleAuthCopilot(_ string, accountLabel string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)
	cfg.Providers.Copilot.Enabled = true
	auth, label, err := resolveOrCreateCopilotAccount(cfg, accountLabel)
	if err != nil {
		return err
	}
	fmt.Printf("Starting GitHub Copilot authentication (account: %s, mode: device_code)...\n", label)
	if err := copilotAuthenticate(auth, func() error { return saveConfig(cfg) }); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	fmt.Println("Authentication successful.")
	return nil
}

func resolveOrCreateCopilotAccount(cfg *Config, label string) (*CopilotAuthState, string, error) {
	accounts := &cfg.Providers.Copilot.Accounts
	if label == "" {
		if len(*accounts) == 0 {
			*accounts = append(*accounts, CopilotAccount{ID: stableAccountID("copilot", "primary"), Label: "primary"})
		}
		label = (*accounts)[0].Label
		return &(*accounts)[0].Auth, label, nil
	}
	for i := range *accounts {
		if (*accounts)[i].Label == label {
			return &(*accounts)[i].Auth, label, nil
		}
	}
	*accounts = append(*accounts, CopilotAccount{ID: stableAccountID("copilot", label), Label: label})
	index := len(*accounts) - 1
	if err := saveConfig(cfg); err != nil {
		return nil, "", fmt.Errorf("failed to add account %q: %w", label, err)
	}
	return &(*accounts)[index].Auth, label, nil
}

// handleAuthCodex stores credentials in ya-router's 0600 config. The official
// Codex store remains read-only import data.
func handleAuthCodex(accountLabel string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)
	cfg.Providers.Codex.Enabled = true
	authPtr, label, err := resolveOrCreateCodexAccount(cfg, accountLabel)
	if err != nil {
		return err
	}
	fmt.Printf("Starting OpenAI Codex authentication (account: %s, mode: chatgpt device_code)...\n", label)
	auth := &CodexAuthState{Mode: "chatgpt"}
	if err := codexAuthenticate(auth, func() error { return nil }); err != nil {
		return fmt.Errorf("Codex authentication failed: %w", err)
	}
	*authPtr = *auth
	clearPersistedChatGPTSecrets(&cfg.Providers.Codex.Auth)
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to persist Codex credentials: %w", err)
	}
	fmt.Println("Codex credentials stored in ya-router's local credential config.")
	return nil
}

func resolveOrCreateCodexAccount(cfg *Config, label string) (*CodexAuthState, string, error) {
	accounts := &cfg.Providers.Codex.Accounts
	if label == "" {
		if len(*accounts) == 0 {
			*accounts = append(*accounts, CodexAccount{ID: stableAccountID("codex", "primary"), Label: "primary"})
		}
		label = (*accounts)[0].Label
		return &(*accounts)[0].Auth, label, nil
	}
	for i := range *accounts {
		if (*accounts)[i].Label == label {
			return &(*accounts)[i].Auth, label, nil
		}
	}
	*accounts = append(*accounts, CodexAccount{ID: stableAccountID("codex", label), Label: label})
	index := len(*accounts) - 1
	if err := saveConfig(cfg); err != nil {
		return nil, "", fmt.Errorf("failed to add Codex account %q: %w", label, err)
	}
	return &(*accounts)[index].Auth, label, nil
}

func handleAuthCodexAPIKey(apiKey, accountLabel string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)
	cfg.Providers.Codex.Enabled = true
	auth, _, err := resolveOrCreateCodexAccount(cfg, accountLabel)
	if err != nil {
		return err
	}
	auth.Mode = "api_key"
	auth.APIKey = apiKey
	auth.AccessToken = ""
	auth.RefreshToken = ""
	auth.ExpiresAt = 0
	auth.AccountID = ""
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Codex API-key mode configured for api.openai.com/v1.")
	return nil
}

func handleAuthCodexManualToken(token, accountLabel string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)
	cfg.Providers.Codex.Enabled = true
	auth, _, err := resolveOrCreateCodexAccount(cfg, accountLabel)
	if err != nil {
		return err
	}
	auth.Mode = "chatgpt"
	auth.AccessToken = token
	auth.RefreshToken = ""
	auth.AccountID = extractAccountIDFromJWT(token)
	auth.ExpiresAt = extractJWTExpiry(token)
	if auth.ExpiresAt == 0 {
		auth.ExpiresAt = time.Now().Unix() + 3600
	}
	clearPersistedChatGPTSecrets(&cfg.Providers.Codex.Auth)
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Codex access token stored in ya-router's local config.")
	fmt.Println("Manual tokens are not refreshable; device authentication is recommended.")
	return nil
}

func handleAuthKiloAnonymous() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cfg.Providers.Kilo.Enabled = true
	cfg.Providers.Kilo.AllowAnonymous = true
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Kilo Gateway enabled in anonymous mode (free model IDs only).")
	return nil
}

func handleAuthKiloAPIKey(apiKey string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cfg.Providers.Kilo.Enabled = true
	cfg.Providers.Kilo.AllowAnonymous = true
	cfg.Providers.Kilo.APIKey = apiKey
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Kilo Gateway API key stored in ya-router's local credential config.")
	return nil
}

func handleStatus() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	path, _ := getConfigPath()
	fmt.Printf("Configuration: %s\n", path)
	fmt.Printf("Port: %d\n\n", cfg.Port)
	if cfg.Providers.Copilot.Enabled {
		printCopilotStatus(&cfg.Providers.Copilot)
	}
	if cfg.Providers.Codex.Enabled {
		printCodexStatus(cfg)
	}
	if cfg.Providers.Kilo.Enabled {
		printKiloStatus(cfg)
	}
	return nil
}

func printCopilotStatus(provider *CopilotProviderConfig) {
	fmt.Println("Provider: GitHub Copilot")
	accounts := provider.Accounts
	if len(accounts) >= 2 {
		fmt.Printf("  Account pool: %d accounts (cooldown: %ds)\n", len(accounts), provider.AccountCooldownSeconds)
		for i := range accounts {
			label := accounts[i].Label
			if label == "" {
				label = fmt.Sprintf("account-%d", i)
			}
			printCopilotAccountLine(i, label, &accounts[i].Auth, accounts[i].LastLimitedAt, provider.AccountCooldownSeconds)
		}
	} else {
		auth := &provider.Auth
		if len(accounts) == 1 {
			auth = &accounts[0].Auth
		}
		printCopilotAuthLines(auth)
	}
	fmt.Println()
}

func printCopilotAccountLine(index int, label string, auth *CopilotAuthState, lastLimitedAt int64, cooldownSeconds int) {
	now := time.Now().Unix()
	prefix := fmt.Sprintf("  [%d] %s:", index, label)
	if lastLimitedAt > 0 && now-lastLimitedAt < int64(cooldownSeconds) {
		fmt.Printf("%s in cooldown (%ds remaining)\n", prefix, int64(cooldownSeconds)-(now-lastLimitedAt))
		return
	}
	if auth.CopilotToken == "" {
		fmt.Printf("%s not authenticated\n", prefix)
		return
	}
	remaining := auth.ExpiresAt - now
	if remaining > 0 {
		fmt.Printf("%s authenticated (expires in %dm %ds)\n", prefix, remaining/60, remaining%60)
	} else {
		fmt.Printf("%s token expired\n", prefix)
	}
}

func printCopilotAuthLines(auth *CopilotAuthState) {
	now := time.Now().Unix()
	if auth.CopilotToken == "" {
		fmt.Printf("  Auth: not authenticated — run '%s auth copilot'\n", os.Args[0])
		return
	}
	remaining := auth.ExpiresAt - now
	if remaining > 0 {
		fmt.Printf("  Auth: authenticated (expires in %dm %ds)\n", remaining/60, remaining%60)
	} else {
		fmt.Printf("  Auth: token expired (%ds ago)\n", -remaining)
	}
	fmt.Printf("  Has GitHub token: %t\n", auth.GitHubToken != "")
}

func printCodexStatus(cfg *Config) {
	fmt.Println("Provider: OpenAI Codex")
	provider := &cfg.Providers.Codex
	accounts := provider.Accounts
	if len(accounts) >= 2 {
		fmt.Printf("  Account pool: %d accounts (cooldown: %ds)\n", len(accounts), provider.AccountCooldownSeconds)
		for i := range accounts {
			label := accounts[i].Label
			if label == "" {
				label = fmt.Sprintf("account-%d", i)
			}
			printCodexAccountLine(i, label, &accounts[i].Auth, accounts[i].LastLimitedAt, provider.AccountCooldownSeconds)
		}
		fmt.Println()
		return
	}
	auth := &provider.Auth
	if len(accounts) == 1 {
		auth = &accounts[0].Auth
	}
	printCodexAuthLines(auth)
	fmt.Println()
}

func printCodexAccountLine(index int, label string, auth *CodexAuthState, lastLimitedAt int64, cooldownSeconds int) {
	now := time.Now().Unix()
	prefix := fmt.Sprintf("  [%d] %s:", index, label)
	if lastLimitedAt > 0 && now-lastLimitedAt < int64(cooldownSeconds) {
		fmt.Printf("%s in cooldown (%ds remaining)\n", prefix, int64(cooldownSeconds)-(now-lastLimitedAt))
		return
	}
	if isAPIKeyMode(auth.Mode) {
		key, source, _ := resolveCodexAPIKey(auth)
		fmt.Printf("%s api_key configured=%t source=%s\n", prefix, key != "", source)
		return
	}
	if auth.AccessToken == "" {
		fmt.Printf("%s not authenticated\n", prefix)
		return
	}
	remaining := auth.ExpiresAt - now
	if auth.ExpiresAt == 0 {
		fmt.Printf("%s authenticated (expiry unavailable)\n", prefix)
	} else if remaining > 0 {
		fmt.Printf("%s authenticated (expires in %dm %ds)\n", prefix, remaining/60, remaining%60)
	} else {
		fmt.Printf("%s token expired\n", prefix)
	}
}

func printCodexAuthLines(auth *CodexAuthState) {
	if isAPIKeyMode(auth.Mode) {
		key, source, err := resolveCodexAPIKey(auth)
		if err != nil {
			fmt.Printf("  Auth: credential lookup failed: %v\n", err)
		} else {
			fmt.Printf("  Auth: API key configured=%t\n", key != "")
			fmt.Printf("  Credential source: %s\n", source)
		}
		fmt.Println("  Backend: api.openai.com/v1")
		fmt.Printf("  Mode: %s\n", auth.Mode)
		return
	}
	resolved := &resolvedCodexChatGPTAuth{
		AccessToken: auth.AccessToken, RefreshToken: auth.RefreshToken,
		ExpiresAt: auth.ExpiresAt, AccountID: auth.AccountID,
		Source: codexCredentialSourceProxyConfig,
	}
	if resolved.AccessToken == "" {
		imported, err := resolveCodexChatGPTAuth(auth)
		if err != nil {
			fmt.Printf("  Auth: credential lookup failed: %v\n", err)
		} else if imported != nil {
			resolved = imported
		}
	}
	if resolved.AccessToken == "" {
		fmt.Printf("  Auth: not authenticated — run '%s auth codex'\n", os.Args[0])
	} else {
		remaining := resolved.ExpiresAt - time.Now().Unix()
		if resolved.ExpiresAt == 0 {
			fmt.Println("  Auth: token available (expiry unavailable)")
		} else if remaining > 0 {
			fmt.Printf("  Auth: authenticated (expires in %dm %ds)\n", remaining/60, remaining%60)
		} else {
			fmt.Printf("  Auth: token expired (%ds ago)\n", -remaining)
		}
		fmt.Printf("  Refreshable: %t\n", resolved.RefreshToken != "")
		fmt.Printf("  Has account metadata: %t\n", resolved.AccountID != "")
		fmt.Printf("  Credential source: %s\n", resolved.Source)
	}
	fmt.Printf("  Mode: %s\n", auth.Mode)
	fmt.Printf("  Backend: %s\n", defaultChatGPTBaseURL)
}

func printKiloStatus(cfg *Config) {
	provider := NewKiloProvider(cfg)
	health := provider.Health(context.Background())
	_, source := provider.apiKey()
	backend, backendErr := provider.baseURL()
	if backendErr != nil {
		backend = "invalid configuration"
	}
	fmt.Println("Provider: Kilo Gateway")
	fmt.Printf("  Ready: %t\n", health.Authenticated)
	fmt.Printf("  Credential source: %s\n", source)
	fmt.Printf("  Anonymous free models: %t\n", cfg.Providers.Kilo.AllowAnonymous)
	fmt.Printf("  Backend: %s\n\n", backend)
}

func handleConfig() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	path, _ := getConfigPath()
	fmt.Printf("Configuration file: %s\n", path)
	fmt.Printf("Config version: %d\n", cfg.ConfigVersion)
	fmt.Printf("Port: %d\n", cfg.Port)
	fmt.Printf("Listen address: %s\n", os.Getenv(listenAddressEnv))
	fmt.Printf("Default model: %s\n", cfg.Routing.DefaultModel)
	fmt.Printf("Default provider: %s\n", cfg.Routing.DefaultProvider)
	fmt.Printf("Copilot enabled: %t\n", cfg.Providers.Copilot.Enabled)
	fmt.Printf("Codex enabled: %t\n", cfg.Providers.Codex.Enabled)
	fmt.Printf("Kilo enabled: %t\n", cfg.Providers.Kilo.Enabled)
	return nil
}

func getCurrentTime() int64 { return time.Now().Unix() }

func handleRunWithMigration(migrationMode ConfigMigrationMode) error {
	if migrationMode != ConfigMigrationNone {
		fmt.Printf("Running config migration (mode: %s)...\n", migrationMode)
		if err := migrateConfig(migrationMode); err != nil {
			return fmt.Errorf("config migration failed: %w", err)
		}
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)
	if cfg.Providers.Codex.Enabled && ensureCodexModelMap(cfg) {
		if err := saveConfig(cfg); err != nil {
			fmt.Printf("Warning: failed to persist Codex model map: %v\n", err)
		}
	}

	runtimeManager, err := runtimepkg.NewManager(cfg)
	if err != nil {
		return fmt.Errorf("create runtime manager: %w", err)
	}
	providerManager, err := newProviderManager(cfg, runtimeManager)
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtimeManager.Close(shutdownContext)
		return fmt.Errorf("create provider manager: %w", err)
	}
	ctx := context.Background()
	for _, provider := range providerManager.ActiveProviders() {
		if err := provider.EnsureAuthenticated(ctx); err != nil {
			fmt.Printf("Warning: provider %s auth failed: %v\n", provider.ID(), err)
		}
	}
	providerManager.RefreshHealth(ctx)
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = providerManager.Reconcile(shutdownContext, nil)
		_ = runtimeManager.Close(shutdownContext)
	}()
	setupLogging()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", managedModelsHandler(runtimeManager))
	mux.HandleFunc("/v1/models/", managedModelsHandler(runtimeManager))
	mux.HandleFunc("/v1/embeddings", managedProxyHandler(runtimeManager))
	mux.HandleFunc("/v1/embeddings/", managedProxyHandler(runtimeManager))
	mux.HandleFunc("/v1/chat/completions", managedProxyHandler(runtimeManager))
	mux.HandleFunc("/v1/chat/completions/", managedProxyHandler(runtimeManager))
	mux.HandleFunc("/v1/responses", managedProxyHandler(runtimeManager))
	mux.HandleFunc("/v1/responses/", managedProxyHandler(runtimeManager))
	mux.HandleFunc("/health", managedHealthHandler(providerManager))
	mux.HandleFunc("/health/", managedHealthHandler(providerManager))
	if cfg.EnablePprof {
		mux.HandleFunc("/debug/pprof/", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/cmdline", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/profile", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/symbol", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/trace", http.DefaultServeMux.ServeHTTP)
	}

	port := cfg.Port
	if port == 0 {
		port = 7071
	}
	address, err := configuredListenAddress(port)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:         address,
		Handler:      secureHandler(mux),
		ReadTimeout:  time.Duration(cfg.Timeouts.ServerRead) * time.Second,
		WriteTimeout: time.Duration(cfg.Timeouts.ServerWrite) * time.Second,
		IdleTimeout:  time.Duration(cfg.Timeouts.ServerIdle) * time.Second,
	}
	setupGracefulShutdown(server)
	fmt.Printf("Starting proxy on %s\n", address)
	fmt.Println("  /v1/models              → aggregated from all providers")
	fmt.Println("  /v1/chat/completions    → Chat Completions compatibility")
	fmt.Println("  /v1/responses           → native Responses API")
	fmt.Println("  /v1/embeddings          → API-key-capable providers only")
	if cfg.EnablePprof {
		fmt.Println("  /debug/pprof/           → enabled and access-controlled")
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

func handleModels(providerFilter string, forceRefresh bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)
	registry := NewProviderRegistry()
	if cfg.Providers.Copilot.Enabled {
		registry.Register(NewCopilotProvider(cfg))
	}
	if cfg.Providers.Codex.Enabled {
		registry.Register(NewCodexProvider(cfg))
	}
	if cfg.Providers.Kilo.Enabled {
		registry.Register(NewKiloProvider(cfg))
	}
	ctx := context.Background()
	for _, provider := range registry.All() {
		if providerFilter != "" && string(provider.ID()) != providerFilter {
			continue
		}
		if forceRefresh {
			invalidateProviderModelCache(provider)
		}
		models, err := provider.ListModels(ctx)
		if err != nil {
			fmt.Printf("[%s] error: %v\n", provider.Name(), err)
			continue
		}
		fmt.Printf("[%s] %d model(s):\n", provider.Name(), len(models.Data))
		for _, model := range models.Data {
			fmt.Printf("  - %s (%s)\n", model.ID, model.OwnedBy)
		}
	}
	return nil
}

func invalidateProviderModelCache(p Provider) {
	type cacheAware interface {
		InvalidateModelCache()
	}
	if cache, ok := p.(cacheAware); ok {
		cache.InvalidateModelCache()
	}
}

func handleRefresh(providerFilter string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)
	if providerFilter == "" || providerFilter == string(ProviderCopilot) {
		if err := refreshCopilot(cfg); err != nil {
			return err
		}
	}
	if (providerFilter == "" || providerFilter == string(ProviderCodex)) && cfg.Providers.Codex.Enabled {
		if err := refreshCodex(cfg); err != nil {
			return err
		}
	}
	if providerFilter == string(ProviderKilo) && cfg.Providers.Kilo.Enabled {
		provider := NewKiloProvider(cfg)
		provider.InvalidateModelCache()
		models, err := provider.ListModels(context.Background())
		if err != nil {
			return err
		}
		fmt.Printf("Kilo model catalog refreshed (%d models).\n", len(models.Data))
	}
	return nil
}

func refreshCopilot(cfg *Config) error {
	accounts := cfg.Providers.Copilot.Accounts
	if len(accounts) > 0 {
		refreshed := 0
		for i := range accounts {
			auth := &accounts[i].Auth
			if auth.CopilotToken == "" {
				fmt.Printf("  [%s] skipping — not authenticated\n", accounts[i].Label)
				continue
			}
			if err := copilotRefreshToken(auth, func() error { return saveConfig(cfg) }); err != nil {
				fmt.Printf("  [%s] refresh failed: %v\n", accounts[i].Label, err)
				continue
			}
			remaining := auth.ExpiresAt - time.Now().Unix()
			fmt.Printf("  [%s] refreshed (expires in %dm %ds)\n", accounts[i].Label, remaining/60, remaining%60)
			refreshed++
		}
		if refreshed == 0 {
			return fmt.Errorf("no Copilot accounts refreshed — run 'auth copilot'")
		}
		return nil
	}
	auth := &cfg.Providers.Copilot.Auth
	if auth.CopilotToken == "" {
		return fmt.Errorf("no Copilot token — run 'auth copilot'")
	}
	if err := copilotRefreshToken(auth, func() error { return saveConfig(cfg) }); err != nil {
		return fmt.Errorf("Copilot refresh failed: %w", err)
	}
	return nil
}

func refreshCodex(cfg *Config) error {
	provider := &cfg.Providers.Codex
	if len(provider.Accounts) > 0 {
		refreshed := 0
		for i := range provider.Accounts {
			account := &provider.Accounts[i]
			auth := &account.Auth
			if isAPIKeyMode(auth.Mode) {
				fmt.Printf("  [%s] API-key mode — no refresh needed\n", account.Label)
				refreshed++
				continue
			}
			if auth.AccessToken == "" {
				fmt.Printf("  [%s] not authenticated — run 'auth codex --account %s'\n", account.Label, account.Label)
				continue
			}
			if auth.RefreshToken == "" {
				fmt.Printf("  [%s] no refresh token — re-authentication required\n", account.Label)
				continue
			}
			if err := codexRefreshToken(auth, func() error { return nil }); err != nil {
				fmt.Printf("  [%s] refresh failed: %v\n", account.Label, err)
				continue
			}
			remaining := auth.ExpiresAt - time.Now().Unix()
			fmt.Printf("  [%s] refreshed (expires in %dm %ds)\n", account.Label, remaining/60, remaining%60)
			refreshed++
		}
		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("persist Codex config: %w", err)
		}
		if refreshed == 0 {
			return fmt.Errorf("no Codex accounts refreshed — run 'auth codex'")
		}
		return nil
	}

	auth := &provider.Auth
	if isAPIKeyMode(auth.Mode) {
		key, _, err := resolveCodexAPIKey(auth)
		if err != nil {
			return err
		}
		if key == "" {
			return fmt.Errorf("no Codex API key configured")
		}
		fmt.Println("Codex API-key mode does not use token refresh.")
		return nil
	}
	if auth.AccessToken == "" {
		resolved, err := resolveCodexChatGPTAuth(auth)
		if err != nil {
			return err
		}
		if resolved != nil {
			applyResolvedCodexChatGPTAuth(auth, resolved)
		}
	}
	if auth.AccessToken == "" || auth.RefreshToken == "" {
		return fmt.Errorf("Codex credentials are not refreshable — run 'auth codex'")
	}
	if err := codexRefreshToken(auth, func() error { return nil }); err != nil {
		return err
	}
	return saveConfig(cfg)
}
