package yarouter

import (
	"context"
	"fmt"
	"strings"
	"time"

	configschema "github.com/duvu/ya-router/internal/config"
	operationpkg "github.com/duvu/ya-router/internal/operation"
	providerpkg "github.com/duvu/ya-router/internal/provider"
	secretpkg "github.com/duvu/ya-router/internal/secret"
)

type authSessionRunner struct {
	operations *operationpkg.Manager
	reloader   mutationReloader
	secrets    secretpkg.SecretStore
}

func (runner authSessionRunner) StartAuthSession(record operationpkg.Record) error {
	if runner.operations == nil {
		return fmt.Errorf("operation manager is unavailable")
	}
	return runner.operations.Run(record.ID, func(ctx context.Context, reporter operationpkg.Reporter) (map[string]string, *operationpkg.Failure) {
		return runner.run(ctx, reporter, record)
	})
}

func (runner authSessionRunner) run(ctx context.Context, reporter operationpkg.Reporter, record operationpkg.Record) (map[string]string, *operationpkg.Failure) {
	providerID := providerpkg.ID(strings.TrimSpace(record.Metadata["provider_id"]))
	method := strings.TrimSpace(record.Metadata["auth_method"])
	if providerID == ProviderCopilot && method == "device_code" {
		credentials, err := runCopilotDeviceFlow(ctx, reporter.WaitingForUser)
		if err != nil {
			failure := operationpkg.NewFailure("device_auth_failed", "The Copilot device authentication did not complete.", true)
			return nil, &failure
		}
		if runner.secrets == nil {
			failure := operationpkg.NewFailure("secret_store_unavailable", "The credential store is unavailable.", true)
			return nil, &failure
		}
		if _, err := runner.secrets.Set(record.Owner, "copilot/github_token", credentials.GitHubToken); err != nil {
			failure := operationpkg.NewFailure("credential_store_failed", "The Copilot credentials could not be stored.", true)
			return nil, &failure
		}
		if _, err := runner.secrets.Set(record.Owner, "copilot/token", credentials.CopilotToken); err != nil {
			failure := operationpkg.NewFailure("credential_store_failed", "The Copilot credentials could not be stored.", true)
			return nil, &failure
		}
		if err := applyCopilotDeviceAuthSession(ctx, record.Owner, runner.reloader); err != nil {
			failure := operationpkg.NewFailure("provider_reconcile_failed", "The provider configuration could not be published.", true)
			return nil, &failure
		}
		return map[string]string{"provider_id": string(providerID), "auth_method": method, "runtime_published": "true"}, nil
	}
	if providerID == ProviderCodex && method == "device_code" {
		credentials, err := runCodexDeviceFlow(ctx, reporter.WaitingForUser)
		if err != nil {
			failure := operationpkg.NewFailure("device_auth_failed", "The Codex device authentication did not complete.", true)
			return nil, &failure
		}
		if runner.secrets == nil {
			failure := operationpkg.NewFailure("secret_store_unavailable", "The credential store is unavailable.", true)
			return nil, &failure
		}
		if _, err := runner.secrets.Set(record.Owner, "codex/access_token", credentials.AccessToken); err != nil {
			failure := operationpkg.NewFailure("credential_store_failed", "The Codex credentials could not be stored.", true)
			return nil, &failure
		}
		if _, err := runner.secrets.Set(record.Owner, "codex/refresh_token", credentials.RefreshToken); err != nil {
			failure := operationpkg.NewFailure("credential_store_failed", "The Codex credentials could not be stored.", true)
			return nil, &failure
		}
		if err := applyCodexDeviceAuthSession(ctx, record.Owner, runner.reloader); err != nil {
			failure := operationpkg.NewFailure("provider_reconcile_failed", "The provider configuration could not be published.", true)
			return nil, &failure
		}
		return map[string]string{"provider_id": string(providerID), "auth_method": method, "runtime_published": "true"}, nil
	}
	if providerID == ProviderCodex && method == "api_key" {
		if !runner.hasCredential("codex/api_key") {
			failure := operationpkg.NewFailure("credential_required", "Set an OpenAI API key through the write-only secret route before starting this session.", false)
			return nil, &failure
		}
		if err := applyCodexAPIKeyAuthSession(ctx, record.Owner, runner.reloader); err != nil {
			failure := operationpkg.NewFailure("provider_reconcile_failed", "The provider configuration could not be published.", true)
			return nil, &failure
		}
		return map[string]string{"provider_id": string(providerID), "auth_method": method, "runtime_published": "true"}, nil
	}
	if providerID != ProviderKilo {
		failure := operationpkg.NewFailure("auth_session_not_implemented", "This provider authentication workflow is not available yet.", false)
		return nil, &failure
	}
	if method != "anonymous" && method != "api_key" {
		failure := operationpkg.NewFailure("unsupported_auth_method", "The authentication method is not supported.", false)
		return nil, &failure
	}
	if err := reporter.Progress(10, map[string]string{"provider_id": string(providerID), "auth_method": method}); err != nil {
		failure := operationpkg.NewFailure("operation_progress_failed", "The authentication operation could not be updated.", true)
		return nil, &failure
	}
	if method == "api_key" && !runner.hasCredential("kilo/api_key") {
		failure := operationpkg.NewFailure("credential_required", "Set a Kilo API key through the write-only secret route before starting this session.", false)
		return nil, &failure
	}
	if err := applyKiloAuthSession(ctx, record.Owner, method, runner.reloader); err != nil {
		failure := operationpkg.NewFailure("provider_reconcile_failed", "The provider configuration could not be published.", true)
		return nil, &failure
	}
	return map[string]string{"provider_id": string(providerID), "auth_method": method, "runtime_published": "true"}, nil
}

func applyCodexDeviceAuthSession(ctx context.Context, actor string, reloader mutationReloader) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	manager := currentConfigState()
	if manager == nil {
		return fmt.Errorf("managed configuration state is not active")
	}
	snapshot := manager.Snapshot()
	next := configschema.Clone(snapshot.Desired)
	next.Providers.Codex.Enabled = true
	next.Providers.Codex.Auth.Mode = "chatgpt"
	applied, err := manager.Apply(snapshot.Revision, next, next, actor, false)
	if err != nil {
		return err
	}
	if reloader == nil {
		return nil
	}
	reconcileContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := reloader.Reconcile(reconcileContext, desiredProviders(applied.Effective)); err == nil {
		return nil
	}
	_, restoreErr := manager.Apply(applied.Revision, snapshot.Desired, snapshot.Effective, actor, false)
	if restoreErr != nil {
		return fmt.Errorf("restore prior configuration: %w", restoreErr)
	}
	return fmt.Errorf("provider reconciliation failed")
}

func applyCopilotDeviceAuthSession(ctx context.Context, actor string, reloader mutationReloader) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	manager := currentConfigState()
	if manager == nil {
		return fmt.Errorf("managed configuration state is not active")
	}
	snapshot := manager.Snapshot()
	next := configschema.Clone(snapshot.Desired)
	next.Providers.Copilot.Enabled = true
	next.Providers.Copilot.Auth.Mode = "device_code"
	applied, err := manager.Apply(snapshot.Revision, next, next, actor, false)
	if err != nil {
		return err
	}
	if reloader == nil {
		return nil
	}
	reconcileContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := reloader.Reconcile(reconcileContext, desiredProviders(applied.Effective)); err == nil {
		return nil
	}
	_, restoreErr := manager.Apply(applied.Revision, snapshot.Desired, snapshot.Effective, actor, false)
	if restoreErr != nil {
		return fmt.Errorf("restore prior configuration: %w", restoreErr)
	}
	return fmt.Errorf("provider reconciliation failed")
}

func (runner authSessionRunner) hasCredential(slot string) bool {
	if runner.secrets != nil {
		if value, _, ok := runner.secrets.Resolve(slot); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	manager := currentConfigState()
	if manager == nil {
		return false
	}
	config := manager.Snapshot().Effective
	if slot == "codex/api_key" {
		return strings.TrimSpace(config.Providers.Codex.Auth.APIKey) != ""
	}
	return strings.TrimSpace(config.Providers.Kilo.APIKey) != ""
}

func applyCodexAPIKeyAuthSession(ctx context.Context, actor string, reloader mutationReloader) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	manager := currentConfigState()
	if manager == nil {
		return fmt.Errorf("managed configuration state is not active")
	}
	snapshot := manager.Snapshot()
	next := configschema.Clone(snapshot.Desired)
	next.Providers.Codex.Enabled = true
	next.Providers.Codex.Auth.Mode = "api_key"
	applied, err := manager.Apply(snapshot.Revision, next, next, actor, false)
	if err != nil {
		return err
	}
	if reloader == nil {
		return nil
	}
	reconcileContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := reloader.Reconcile(reconcileContext, desiredProviders(applied.Effective)); err == nil {
		return nil
	}
	_, restoreErr := manager.Apply(applied.Revision, snapshot.Desired, snapshot.Effective, actor, false)
	if restoreErr != nil {
		return fmt.Errorf("restore prior configuration: %w", restoreErr)
	}
	return fmt.Errorf("provider reconciliation failed")
}

func applyKiloAuthSession(ctx context.Context, actor, method string, reloader mutationReloader) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	manager := currentConfigState()
	if manager == nil {
		return fmt.Errorf("managed configuration state is not active")
	}
	snapshot := manager.Snapshot()
	next := configschema.Clone(snapshot.Desired)
	next.Providers.Kilo.Enabled = true
	next.Providers.Kilo.AllowAnonymous = method == "anonymous"
	applied, err := manager.Apply(snapshot.Revision, next, next, actor, false)
	if err != nil {
		return err
	}
	if reloader == nil {
		return nil
	}
	reconcileContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := reloader.Reconcile(reconcileContext, desiredProviders(applied.Effective)); err == nil {
		return nil
	}
	_, restoreErr := manager.Apply(applied.Revision, snapshot.Desired, snapshot.Effective, actor, false)
	if restoreErr != nil {
		return fmt.Errorf("restore prior configuration: %w", restoreErr)
	}
	return fmt.Errorf("provider reconciliation failed")
}
