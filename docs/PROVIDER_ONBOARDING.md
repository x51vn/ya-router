# Provider onboarding contract

Additional providers are compiled Go adapters. They do not require router,
Control API, or TUI-specific branches when they use the existing provider
factory and descriptor contracts.

1. Implement the `internal/provider.Provider` interface in `src/` and keep
   provider-specific auth, request construction, retries, and error mapping in
   that adapter.
2. Register a `provider.Factory` in `compiledProviderFactoriesWithAuth` with a
   stable ID, display name, endpoint capabilities, auth modes, and redacted
   configuration schema. Secret fields must be marked `Secret`.
3. Normalize upstream model catalogs to bare upstream IDs. The existing model
   surface adds provider prefixes, retains last-known-good catalog data, and
   applies allowlists.
4. Return typed `ProviderError` values for operational failures. Unsupported
   endpoint semantics must be omitted from the capability declaration or fail
   explicitly; an adapter must not silently discard them.
5. Add provider-specific mock-upstream tests and run the reusable descriptor
   conformance tests in `src/provider_conformance_test.go`. Normal CI must not
   use live provider credentials.
6. Add a configured `routing.virtual_models` candidate only when the provider's
   declared capability and catalog make it eligible. The selector evaluates
   health, authentication, allowlists, catalog freshness, and cooldown before
   dispatching one pinned target.
7. Verify the provider appears automatically in the redacted Control API and
   `ya` dashboard. Do not expose credentials, prompts, completions, or raw
   upstream errors through either surface.

The current GitHub Copilot, Codex, and Kilo adapters are the baseline. A
runtime plugin system or external provider SDK is intentionally out of scope.
