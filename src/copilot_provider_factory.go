package yarouter

import (
	"time"

	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func NewCopilotProvider(cfg *Config) *CopilotProvider {
	return NewCopilotProviderWithAuth(cfg, nil)
}

func NewCopilotProviderWithAuth(cfg *Config, auth secretpkg.AuthController) *CopilotProvider {
	timeout := time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second
	provider := &CopilotProvider{
		cfg:         cfg,
		auth:        auth,
		cb:          &CircuitBreaker{state: CircuitClosed, timeout: timeout},
		mc:          NewCoalescingCache(),
		cache:       NewModelCache(defaultModelCacheTTL),
		freeCatalog: NewCopilotFreeCatalog(defaultFreeCatalogTTL),
	}
	provider.accountCursor = provider.firstHealthyAccount()
	return provider
}
