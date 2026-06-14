## Why

Native Codex `/backend-api/codex/responses` requests currently fail with HTTP 400 because the proxy does not explicitly enforce the upstream requirement that `store` must be `false`. This breaks `/v1/responses` calls for `oc-*` models in production and needs a targeted spec-backed fix so compatibility logic and native Codex request shaping remain aligned.

## What Changes

- Enforce `store=false` whenever the service proxies native Codex Responses requests to the ChatGPT Codex backend.
- Preserve compatibility for OpenAI-style `/v1/responses` clients by accepting omitted `store` values and normalizing explicit values before upstream dispatch.
- Add regression coverage for Codex native responses request construction and proxy behavior around `store` handling.
- Document the request-shaping rule through OpenSpec artifacts so later adapter work does not regress the Codex backend contract.

## Capabilities

### New Capabilities
- `codex-responses-store-enforcement`: normalize OpenAI-compatible responses requests so ChatGPT Codex upstream always receives `store: false`.

### Modified Capabilities
None.

## Impact

- Affected code: `src/responses_adapter.go`, `src/codex_provider.go`, `src/proxy.go`, and related tests.
- Affected API: `/v1/responses` for Codex-routed models, especially `oc-gpt-5.4`.
- Affected operations: upstream ChatGPT Codex backend calls at `/backend-api/codex/responses` will stop failing on missing or incorrect `store` values.
