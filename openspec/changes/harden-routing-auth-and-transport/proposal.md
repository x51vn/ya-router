# Change: Harden routing, authentication, and provider transport boundaries

## Summary

`ya-router` currently exposes a useful OpenAI-compatible proxy, but several request paths bypass the declared router contract, ChatGPT OAuth credentials can cross into OpenAI Platform endpoints, multi-account Codex credentials are not isolated reliably, the official Codex credential file is mutated by the proxy, structured-output requests are silently downgraded, and CI does not block on test failures.

This change hardens the production Go runtime before additional provider or Rust-port work proceeds.

## Current status

- The Go runtime in `src/` remains the production implementation.
- Provider prefixes and `routing.model_map` exist, but Copilot free-chat dispatch currently runs before route resolution.
- Codex has distinct ChatGPT and API-key upstream URLs, but fallback and embeddings paths can still send a ChatGPT bearer token to Platform endpoints.
- Codex multi-account support exists, but global official-store precedence can replace a selected account's credentials.
- ChatGPT requests are translated to Responses API, but JSON Schema structured-output semantics are dropped.
- The HTTP server binds all interfaces by default and has no inbound authentication.
- CI runs unit tests with `|| true`.

## Goals

1. Make routing deterministic: explicit model maps and `gc-`/`oc-` prefixes MUST be resolved before provider-specific optimizations.
2. Keep authentication domains separate: ChatGPT OAuth credentials MUST never be sent to `api.openai.com`.
3. Isolate Codex account credentials and stop mutating the official Codex credential store.
4. Add safe token expiry, refresh, and bounded 401 recovery.
5. Preserve structured-output requests instead of silently dropping them.
6. Add a native `/v1/responses` path for clients that do not need Chat Completions compatibility translation.
7. Default the server to loopback, require an inbound API key for non-loopback binding, and remove wildcard CORS.
8. Return and log accurate upstream failure status.
9. Make CI validation blocking.

## Non-goals

- Adding trading, browser-cookie scraping, or undocumented session extraction.
- Automatically sharing or rotating credentials across users.
- Completing the Rust cutover in this change.
- Adding new provider families.
- Guaranteeing undocumented ChatGPT backend compatibility indefinitely.

## Compatibility and migration

- Existing single-account configs remain readable.
- Legacy ChatGPT credentials in the official Codex store may be imported read-only when the configured account has no router-owned credential.
- Existing `/v1/chat/completions`, `/v1/models`, `/v1/embeddings`, and `/health` paths remain available.
- The default listen address changes from all interfaces to `127.0.0.1`. Operators intentionally exposing the service must set `YA_ROUTER_LISTEN_ADDRESS` and `YA_ROUTER_API_KEY`.
- ChatGPT-authenticated Codex no longer advertises or attempts embeddings through Platform endpoints.

## Dependencies

- This change supersedes runtime assumptions in the completed Codex transport remediation and tightens the completed Codex multi-account implementation.
- The Rust port must consume the requirements in this change rather than copying the pre-hardening Go behavior.
