package yarouter

import (
	"context"
	"net/http"
	"time"
)

func (p *CopilotProvider) EnsureAuthenticated(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	auth := p.authState()
	token := auth.CopilotToken
	if p.auth != nil {
		if credential, ok := p.auth.ResolveCredential("copilot/token"); ok {
			token = credential.Value
		}
	}
	if token == "" {
		return newProviderError(ProviderCopilot, ProviderErrorAuthRequired, http.StatusUnauthorized, false,
			"Copilot authentication is required")
	}

	now := time.Now().Unix()
	threshold := int64(300)
	if auth.RefreshIn > 0 {
		if refreshThreshold := auth.RefreshIn / 5; refreshThreshold > threshold {
			threshold = refreshThreshold
		}
	}
	if auth.ExpiresAt > 0 && auth.ExpiresAt-now <= threshold {
		return newProviderError(ProviderCopilot, ProviderErrorAuthRequired, http.StatusUnauthorized, false,
			"Copilot authentication requires reconnection")
	}
	return nil
}
