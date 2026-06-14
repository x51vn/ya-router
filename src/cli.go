// cli.go — command-line command handlers.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"
)

func printUsage() {
	fmt.Printf("GitHub Copilot SVCS Proxy\n\n")
	fmt.Printf("Usage: %s [command] [options]\n\n", os.Args[0])
	fmt.Printf("Commands:\n")
	fmt.Printf("  run|start             Start the proxy server\n")
	fmt.Printf("    --config-migrate    Config migration mode: merge (default), none, override\n")
	fmt.Printf("  auth [copilot|codex]  Authenticate a provider (default: copilot)\n")
	fmt.Printf("    --mode              Auth mode: device_code (default)\n")
	fmt.Printf("    --account <label>   Account label for multi-account pool\n")
	fmt.Printf("    --api-key <key>     Use OpenAI Platform API key (codex only)\n")
	fmt.Printf("    --token <token>     Manually set access token (codex only, fallback)\n")
	fmt.Printf("  status                Show authentication status for all providers\n")
	fmt.Printf("  config                Show current configuration\n")
	fmt.Printf("  models [--provider P] List models (all providers or a specific one)\n")
	fmt.Printf("  refresh [--provider P] Force token refresh\n")
	fmt.Printf("  migrate-config        Migrate configuration file\n")
	fmt.Printf("    --mode              Migration mode: merge (default), override\n")
	fmt.Printf("  version               Show version information\n")
	fmt.Printf("  help                  Show this help\n\n")
	flag.PrintDefaults()
}

// handleAuthCopilot runs the GitHub device-flow for Copilot.
// mode is ignored for Copilot (always device_code) but accepted for CLI consistency.
// accountLabel selects which pool entry to authenticate; empty = first account.
func handleAuthCopilot(mode, accountLabel string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)
	cfg.Providers.Copilot.Enabled = true

	auth, label, saveErr := resolveOrCreateCopilotAccount(cfg, accountLabel)
	if saveErr != nil {
		return saveErr
	}

	fmt.Printf("Starting GitHub Copilot authentication (account: %s, mode: device_code)...\n", label)
	if err := copilotAuthenticate(auth, func() error { return saveConfig(cfg) }); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	fmt.Println("Authentication successful!")
	return nil
}

// resolveOrCreateCopilotAccount returns a pointer into cfg's account pool for the
// given label. If label is empty the first account is used. If no matching
// account exists a new one is appended and the config is saved.
func resolveOrCreateCopilotAccount(cfg *Config, label string) (*CopilotAuthState, string, error) {
	accounts := &cfg.Providers.Copilot.Accounts

	if label == "" {
		if len(*accounts) == 0 {
			*accounts = append(*accounts, CopilotAccount{Label: "primary"})
		}
		label = (*accounts)[0].Label
		return &(*accounts)[0].Auth, label, nil
	}

	for i := range *accounts {
		if (*accounts)[i].Label == label {
			return &(*accounts)[i].Auth, label, nil
		}
	}

	*accounts = append(*accounts, CopilotAccount{Label: label})
	idx := len(*accounts) - 1
	if err := saveConfig(cfg); err != nil {
		return nil, "", fmt.Errorf("failed to add account %q: %w", label, err)
	}
	return &(*accounts)[idx].Auth, label, nil
}

// handleAuthCodex runs the OpenAI OAuth device-code flow for Codex and persists
// ChatGPT-backed credentials to the official Codex auth store.
func handleAuthCodex(accountLabel string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)

	cfg.Providers.Codex.Enabled = true

	authPtr, label, resolveErr := resolveOrCreateCodexAccount(cfg, accountLabel)
	if resolveErr != nil {
		return resolveErr
	}
	authPtr.Mode = "chatgpt"

	fmt.Printf("Starting OpenAI Codex authentication (account: %s, mode: chatgpt device_code)...\n", label)

	auth := &CodexAuthState{Mode: "chatgpt"}
	if err := codexAuthenticate(auth, func() error { return nil }); err != nil {
		return fmt.Errorf("Codex authentication failed: %w", err)
	}

	if err := persistToOfficialStore(auth); err != nil {
		fmt.Printf("Warning: could not write to official Codex store: %v\n", err)
	} else {
		p, _ := officialAuthJSONPath()
		fmt.Printf("Tokens also saved to %s\n", p)
	}
	authPtr.AccessToken = auth.AccessToken
	authPtr.RefreshToken = auth.RefreshToken
	authPtr.ExpiresAt = auth.ExpiresAt
	authPtr.AccountID = auth.AccountID
	clearPersistedChatGPTSecrets(&cfg.Providers.Codex.Auth)
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to persist Codex auth mode: %w", err)
	}

	fmt.Println("Codex credentials validated successfully!")
	return nil
}

func resolveOrCreateCodexAccount(cfg *Config, label string) (*CodexAuthState, string, error) {
	accounts := &cfg.Providers.Codex.Accounts

	if label == "" {
		if len(*accounts) == 0 {
			*accounts = append(*accounts, CodexAccount{Label: "primary"})
		}
		label = (*accounts)[0].Label
		return &(*accounts)[0].Auth, label, nil
	}

	for i := range *accounts {
		if (*accounts)[i].Label == label {
			return &(*accounts)[i].Auth, label, nil
		}
	}

	*accounts = append(*accounts, CodexAccount{Label: label})
	idx := len(*accounts) - 1
	if err := saveConfig(cfg); err != nil {
		return nil, "", fmt.Errorf("failed to add codex account %q: %w", label, err)
	}
	return &(*accounts)[idx].Auth, label, nil
}

// handleAuthCodexAPIKey stores an OpenAI Platform API key for Codex.
func handleAuthCodexAPIKey(apiKey, accountLabel string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)

	cfg.Providers.Codex.Enabled = true
	authPtr, _, resolveErr := resolveOrCreateCodexAccount(cfg, accountLabel)
	if resolveErr != nil {
		return resolveErr
	}
	authPtr.Mode = "api_key"
	authPtr.APIKey = apiKey
	authPtr.AccessToken = ""
	authPtr.RefreshToken = ""
	authPtr.ExpiresAt = 0
	authPtr.AccountID = ""

	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Codex API key saved (mode: api_key → api.openai.com/v1).")
	return nil
}

// handleAuthCodexManualToken sets a manually-provided access token.
// Useful as a fallback for environments where device-code flow fails.
func handleAuthCodexManualToken(token, accountLabel string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)

	cfg.Providers.Codex.Enabled = true
	authPtr, _, resolveErr := resolveOrCreateCodexAccount(cfg, accountLabel)
	if resolveErr != nil {
		return resolveErr
	}
	authPtr.Mode = "chatgpt"
	auth := &CodexAuthState{Mode: "chatgpt", AccessToken: token, ExpiresAt: time.Now().Unix() + 86400}
	if err := persistToOfficialStore(auth); err != nil {
		return fmt.Errorf("failed to persist official Codex auth store: %w", err)
	}
	authPtr.AccessToken = auth.AccessToken
	authPtr.ExpiresAt = auth.ExpiresAt
	clearPersistedChatGPTSecrets(&cfg.Providers.Codex.Auth)

	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Codex access token saved successfully!")
	fmt.Println("Note: token expiry is treated as 24h for this manual fallback. Run 'refresh --provider codex' later if needed.")
	return nil
}

// handleStatus prints authentication status for each configured provider.
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
	return nil
}

func printCopilotStatus(pCfg *CopilotProviderConfig) {
	fmt.Println("Provider: GitHub Copilot")
	accounts := pCfg.Accounts
	if len(accounts) >= 2 {
		fmt.Printf("  Account pool: %d accounts (cooldown: %ds)\n",
			len(accounts), pCfg.AccountCooldownSeconds)
		for i, acc := range accounts {
			label := acc.Label
			if label == "" {
				label = fmt.Sprintf("account-%d", i)
			}
			printCopilotAccountLine(i, label, &acc.Auth, acc.LastLimitedAt, pCfg.AccountCooldownSeconds)
		}
	} else {
		var auth *CopilotAuthState
		if len(accounts) == 1 {
			auth = &accounts[0].Auth
		} else {
			auth = &pCfg.Auth
		}
		printCopilotAuthLines(auth)
	}
	fmt.Println()
}

func printCopilotAccountLine(idx int, label string, auth *CopilotAuthState, lastLimitedAt int64, cooldownSecs int) {
	now := time.Now().Unix()
	prefix := fmt.Sprintf("  [%d] %s:", idx, label)
	if lastLimitedAt > 0 {
		elapsed := now - lastLimitedAt
		if elapsed < int64(cooldownSecs) {
			fmt.Printf("%s ⏳ in cooldown (%ds remaining)\n", prefix, int64(cooldownSecs)-elapsed)
			return
		}
	}
	if auth.CopilotToken == "" {
		fmt.Printf("%s ✗ not authenticated\n", prefix)
		return
	}
	remaining := auth.ExpiresAt - now
	if remaining > 0 {
		fmt.Printf("%s ✓ authenticated (expires in %dm %ds)\n", prefix, remaining/60, remaining%60)
	} else {
		fmt.Printf("%s ⚠ token expired\n", prefix)
	}
}

func printCopilotAuthLines(auth *CopilotAuthState) {
	now := time.Now().Unix()
	if auth.CopilotToken != "" {
		remaining := auth.ExpiresAt - now
		if remaining > 0 {
			fmt.Printf("  Auth: ✓ Authenticated (expires in %dm %ds)\n", remaining/60, remaining%60)
			threshold := int64(300)
			if auth.RefreshIn > 0 {
				if t := auth.RefreshIn / 5; t > threshold {
					threshold = t
				}
			}
			if remaining <= threshold {
				fmt.Printf("  Status: ⚠  Refresh imminent\n")
			} else {
				fmt.Printf("  Status: ✅ Token healthy\n")
			}
		} else {
			fmt.Printf("  Auth: ⚠  Token EXPIRED (%d s ago)\n", -remaining)
		}
		fmt.Printf("  Has GitHub token: %t\n", auth.GitHubToken != "")
	} else {
		fmt.Printf("  Auth: ✗ Not authenticated — run '%s auth copilot'\n", os.Args[0])
	}
}

func printCodexStatus(cfg *Config) {
	fmt.Println("Provider: OpenAI Codex")
	pCfg := &cfg.Providers.Codex
	accounts := pCfg.Accounts

	if len(accounts) >= 2 {
		fmt.Printf("  Account pool: %d accounts (cooldown: %ds)\n",
			len(accounts), pCfg.AccountCooldownSeconds)
		for i, acc := range accounts {
			label := acc.Label
			if label == "" {
				label = fmt.Sprintf("account-%d", i)
			}
			printCodexAccountLine(i, label, &acc.Auth, acc.LastLimitedAt, pCfg.AccountCooldownSeconds)
		}
		fmt.Println()
		return
	}

	var auth *CodexAuthState
	if len(accounts) == 1 {
		auth = &accounts[0].Auth
	} else {
		auth = &pCfg.Auth
	}
	printCodexAuthLines(auth)
	fmt.Println()
}

func printCodexAccountLine(idx int, label string, auth *CodexAuthState, lastLimitedAt int64, cooldownSecs int) {
	now := time.Now().Unix()
	prefix := fmt.Sprintf("  [%d] %s:", idx, label)
	if lastLimitedAt > 0 {
		elapsed := now - lastLimitedAt
		if elapsed < int64(cooldownSecs) {
			fmt.Printf("%s ⏳ in cooldown (%ds remaining)\n", prefix, int64(cooldownSecs)-elapsed)
			return
		}
	}
	if isAPIKeyMode(auth.Mode) {
		if auth.APIKey != "" {
			fmt.Printf("%s ✓ API key configured (mode: api_key)\n", prefix)
		} else {
			fmt.Printf("%s ✗ not authenticated (mode: api_key)\n", prefix)
		}
		return
	}
	if auth.AccessToken == "" {
		fmt.Printf("%s ✗ not authenticated\n", prefix)
		return
	}
	if auth.ExpiresAt > 0 {
		remaining := auth.ExpiresAt - now
		if remaining > 0 {
			fmt.Printf("%s ✓ authenticated (expires in %dm %ds)\n", prefix, remaining/60, remaining%60)
		} else {
			fmt.Printf("%s ⚠ token expired\n", prefix)
		}
	} else {
		fmt.Printf("%s ✓ authenticated (no expiry info)\n", prefix)
	}
}

func printCodexAuthLines(auth *CodexAuthState) {
	now := time.Now().Unix()
	if isAPIKeyMode(auth.Mode) {
		key, source, err := resolveCodexAPIKey(auth)
		if err != nil {
			fmt.Printf("  Auth: ⚠  Credential lookup failed: %v\n", err)
		} else if key != "" {
			fmt.Printf("  Auth: ✓ API key configured\n")
			fmt.Printf("  Credential source: %s\n", source)
			fmt.Printf("  Backend: api.openai.com/v1\n")
		} else {
			fmt.Printf("  Auth: ✗ No API key — run '%s auth codex --api-key <key>'\n", os.Args[0])
		}
		fmt.Printf("  Mode: %s\n", auth.Mode)
		return
	}

	resolved, err := resolveCodexChatGPTAuth(auth)
	if err != nil {
		fmt.Printf("  Auth: ⚠  Credential lookup failed: %v\n", err)
		fmt.Printf("  Mode: %s\n", auth.Mode)
		fmt.Printf("  Backend: %s\n", defaultChatGPTBaseURL)
		return
	}

	if resolved != nil {
		if resolved.ExpiresAt > 0 {
			remaining := resolved.ExpiresAt - now
			if remaining > 0 {
				fmt.Printf("  Auth: ✓ Authenticated (expires in %dm %ds)\n",
					remaining/60, remaining%60)
				if remaining <= 300 {
					fmt.Printf("  Status: ⚠  Refresh imminent\n")
				} else {
					fmt.Printf("  Status: ✅ Token healthy\n")
				}
			} else {
				fmt.Printf("  Auth: ⚠  Token EXPIRED (%d s ago)\n", -remaining)
			}
		} else {
			fmt.Printf("  Auth: ✓ Token available (no expiry info)\n")
		}
		fmt.Printf("  Refreshable: %t\n", resolved.RefreshToken != "")
		fmt.Printf("  Has account metadata: %t\n", resolved.AccountID != "")
		fmt.Printf("  Credential source: %s\n", resolved.Source)
	} else {
		fmt.Printf("  Auth: ✗ Not authenticated — run '%s auth codex'\n", os.Args[0])
	}
	fmt.Printf("  Mode: %s\n", auth.Mode)
	fmt.Printf("  Backend: %s\n", defaultChatGPTBaseURL)
}

// handleConfig prints the current configuration.
func handleConfig() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	path, _ := getConfigPath()
	fmt.Printf("Configuration file: %s\n", path)
	fmt.Printf("Config version: %d\n", cfg.ConfigVersion)
	fmt.Printf("Port: %d\n", cfg.Port)
	fmt.Printf("Default model: %s\n", cfg.Routing.DefaultModel)
	fmt.Printf("Default provider: %s\n", cfg.Routing.DefaultProvider)
	fmt.Printf("Copilot enabled: %t\n", cfg.Providers.Copilot.Enabled)
	fmt.Printf("Codex enabled: %t\n", cfg.Providers.Codex.Enabled)
	fmt.Printf("Copilot allowed models: %v\n", cfg.Providers.Copilot.AllowedModels)
	return nil
}

func getCurrentTime() int64 {
	return time.Now().Unix()
}

// handleRunWithMigration migrates config then starts the proxy server.
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

	// Ensure codex known models are in model_map (persisted to config).
	if cfg.Providers.Codex.Enabled {
		if ensureCodexModelMap(cfg) {
			if err := saveConfig(cfg); err != nil {
				fmt.Printf("Warning: failed to persist codex model_map: %v\n", err)
			}
		}
	}

	// Build provider registry.
	registry := NewProviderRegistry()
	if cfg.Providers.Copilot.Enabled {
		registry.Register(NewCopilotProvider(cfg))
	}
	if cfg.Providers.Codex.Enabled {
		registry.Register(NewCodexProvider(cfg))
	}

	// Eagerly authenticate; non-fatal per provider.
	ctx := context.Background()
	for _, p := range registry.All() {
		if err := p.EnsureAuthenticated(ctx); err != nil {
			fmt.Printf("Warning: provider %s auth failed: %v\n", p.ID(), err)
		}
	}

	router := NewModelRouter(registry, cfg.Routing)

	setupLogging()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", modelsHandler(registry, cfg))
	mux.HandleFunc("/v1/models/", modelsHandler(registry, cfg))
	mux.HandleFunc("/v1/embeddings", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/v1/embeddings/", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/v1/chat/completions", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/v1/chat/completions/", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/v1/responses", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/v1/responses/", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/health/", healthHandler)
	if cfg.EnablePprof {
		mux.HandleFunc("/debug/pprof/", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/cmdline", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/profile", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/symbol", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/trace", http.DefaultServeMux.ServeHTTP)
	}

	port := cfg.Port
	if port == 0 {
		port = 8081
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  time.Duration(cfg.Timeouts.ServerRead) * time.Second,
		WriteTimeout: time.Duration(cfg.Timeouts.ServerWrite) * time.Second,
		IdleTimeout:  time.Duration(cfg.Timeouts.ServerIdle) * time.Second,
	}
	setupGracefulShutdown(server)

	fmt.Printf("Starting proxy on :%d\n", port)
	fmt.Printf("  /v1/models              → aggregated from all providers\n")
	fmt.Printf("  /v1/chat/completions    → routed per model\n")
	fmt.Printf("  /v1/responses           → routed per model\n")
	fmt.Printf("  /v1/embeddings          → routed per model\n")
	if cfg.EnablePprof {
		fmt.Printf("  /debug/pprof/           → enabled\n")
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// handleModels lists models from the given provider (or all providers).
func handleModels(providerFilter string) error {
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

	ctx := context.Background()
	for _, p := range registry.All() {
		if providerFilter != "" && string(p.ID()) != providerFilter {
			continue
		}
		ml, err := p.ListModels(ctx)
		if err != nil {
			fmt.Printf("[%s] error: %v\n", p.Name(), err)
			continue
		}
		fmt.Printf("[%s] %d model(s):\n", p.Name(), len(ml.Data))
		for _, m := range ml.Data {
			fmt.Printf("  - %s (%s)\n", m.ID, m.OwnedBy)
		}
	}
	return nil
}

// handleRefresh forces a token refresh for Copilot (and/or Codex when applicable).
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
			fmt.Printf("  [%s] ✅ refreshed (expires in %dm %ds)\n", accounts[i].Label, remaining/60, remaining%60)
			refreshed++
		}
		if refreshed == 0 {
			return fmt.Errorf("no Copilot accounts refreshed — run 'auth copilot' to authenticate")
		}
		return nil
	}
	auth := &cfg.Providers.Copilot.Auth
	if auth.CopilotToken == "" {
		return fmt.Errorf("no Copilot token - run 'auth copilot' first")
	}
	fmt.Println("Refreshing Copilot token...")
	if err := copilotRefreshToken(auth, func() error { return saveConfig(cfg) }); err != nil {
		return fmt.Errorf("Copilot refresh failed: %w", err)
	}
	remaining := auth.ExpiresAt - time.Now().Unix()
	fmt.Printf("✅ Copilot token refreshed (expires in %dm %ds)\n", remaining/60, remaining%60)
	return nil
}

func refreshCodex(cfg *Config) error {
	pCfg := &cfg.Providers.Codex
	accounts := pCfg.Accounts

	if len(accounts) > 0 {
		refreshed := 0
		for i := range accounts {
			auth := &accounts[i].Auth
			if isAPIKeyMode(auth.Mode) {
				fmt.Printf("  [%s] API key mode — no token refresh needed\n", accounts[i].Label)
				refreshed++
				continue
			}
			working := *auth
			resolved, err := resolveCodexChatGPTAuth(auth)
			if err != nil {
				fmt.Printf("  [%s] credential lookup failed: %v\n", accounts[i].Label, err)
				continue
			}
			if resolved != nil {
				applyResolvedCodexChatGPTAuth(&working, resolved)
			}
			if working.AccessToken == "" {
				fmt.Printf("  [%s] not authenticated — run 'auth codex --account %s' first\n",
					accounts[i].Label, accounts[i].Label)
				continue
			}
			if working.RefreshToken == "" {
				fmt.Printf("  [%s] no refresh token — re-authenticating...\n", accounts[i].Label)
				if err := handleAuthCodex(accounts[i].Label); err != nil {
					fmt.Printf("  [%s] re-auth failed: %v\n", accounts[i].Label, err)
				}
				continue
			}
			if err := codexRefreshToken(&working, func() error { return nil }); err != nil {
				fmt.Printf("  [%s] refresh failed: %v\n", accounts[i].Label, err)
				continue
			}
			if err := persistToOfficialStore(&working); err != nil {
				fmt.Printf("  [%s] persist failed: %v\n", accounts[i].Label, err)
				continue
			}
			auth.AccessToken = working.AccessToken
			auth.RefreshToken = working.RefreshToken
			auth.ExpiresAt = working.ExpiresAt
			auth.AccountID = working.AccountID
			remaining := auth.ExpiresAt - time.Now().Unix()
			fmt.Printf("  [%s] ✅ refreshed (expires in %dm %ds)\n", accounts[i].Label, remaining/60, remaining%60)
			refreshed++
		}
		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("persist Codex config: %w", err)
		}
		if refreshed == 0 {
			return fmt.Errorf("no Codex accounts refreshed — run 'auth codex' to authenticate")
		}
		return nil
	}

	auth := &pCfg.Auth
	if isAPIKeyMode(auth.Mode) {
		key, _, err := resolveCodexAPIKey(auth)
		if err != nil {
			return err
		}
		if key == "" {
			return fmt.Errorf("no Codex API key - run 'auth codex --api-key' first")
		}
		fmt.Println("Codex API key mode does not use token refresh.")
		return nil
	}

	working := *auth
	resolved, err := resolveCodexChatGPTAuth(auth)
	if err != nil {
		return err
	}
	if resolved != nil {
		applyResolvedCodexChatGPTAuth(&working, resolved)
	}
	if working.AccessToken == "" {
		return fmt.Errorf("no Codex token — run 'auth codex' first")
	}
	if working.RefreshToken == "" {
		fmt.Println("Codex: no refresh token available, re-authenticating...")
		return handleAuthCodex("")
	}
	fmt.Println("Refreshing Codex token...")
	if err := codexRefreshToken(&working, func() error { return nil }); err != nil {
		fmt.Printf("Refresh failed: %v — re-authenticating...\n", err)
		return handleAuthCodex("")
	}
	if err := persistToOfficialStore(&working); err != nil {
		return fmt.Errorf("persist refreshed Codex credentials: %w", err)
	}
	clearPersistedChatGPTSecrets(auth)
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("persist Codex config: %w", err)
	}
	remaining := working.ExpiresAt - time.Now().Unix()
	fmt.Printf("✅ Codex token refreshed (expires in %dm %ds)\n", remaining/60, remaining%60)
	return nil
}
