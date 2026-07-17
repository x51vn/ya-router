package yarouter

import (
	"time"

	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func NewCodexProvider(cfg *Config) *CodexProvider {
	return NewCodexProviderWithAuth(cfg, nil)
}

func NewCodexProviderWithAuth(cfg *Config, auth secretpkg.AuthController) *CodexProvider {
	provider := &CodexProvider{
		cfg:  cfg,
		auth: auth,
		cb: &CircuitBreaker{
			state:   CircuitClosed,
			timeout: time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second,
		},
		cache: NewModelCache(defaultModelCacheTTL),
	}
	provider.accountCursor = provider.firstHealthyCodexAccount()
	return provider
}
