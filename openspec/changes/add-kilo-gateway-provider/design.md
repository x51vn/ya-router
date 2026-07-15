# Design: Kilo Gateway provider

## Provider boundary

`KiloProvider` implements the existing `Provider` interface and owns Kilo authentication, model discovery, request headers, endpoint selection, circuit breaking, and retry behavior. The shared proxy only sees `ProviderKilo`, `CapabilityChat`, and `CapabilityResponses`.

## Routing

The client-facing prefix is `kilo/`. For example, `kilo/kilo-auto/free` resolves only to Kilo and is forwarded as `kilo-auto/free`. It never falls through to Copilot or Codex.

## Credentials

Credential precedence is:

1. `KILO_API_KEY`;
2. an API key imported into ya-router's `0600` config;
3. anonymous access when explicitly allowed.

The inbound ya-router authorization header is discarded. Only the server-owned Kilo credential is attached upstream. `KILO_ORG_ID` overrides the configured organization ID.

## Anonymous free mode

Model discovery is public. Without an API key, the provider exposes only catalog entries marked free, zero-priced entries, IDs ending in `:free`, `openrouter/free`, and `kilo-auto/free`. The stable Auto Free ID is synthesized if the public catalog omits it. A paid model routed through `model_map` is rejected before network delivery.

## Transport

- `GET {base}/models` performs discovery.
- Chat Completions use `POST {base}/chat/completions`.
- native Responses use `POST {base}/responses`.
- HTTP status, response headers, JSON bodies, and SSE events pass through unchanged.
- Unsafe POST retry remains gated by `Idempotency-Key`.
- Custom base URLs require HTTPS except for loopback test/development endpoints.

## Privacy

Auto Free may route through providers with prompt/output logging or training policies. User-facing documentation must warn operators not to send confidential, personal, or regulated data.

