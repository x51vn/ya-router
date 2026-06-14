## Context

The service currently exposes `/v1/models`, `/v1/chat/completions`, `/v1/embeddings`, and `/health`, with request routing centered around model-to-provider resolution in `proxy.go` and provider abstractions in `provider.go`. The codebase already contains `responses_adapter.go`, which converts Chat Completions payloads into transport-specific Responses API requests and can normalize parts of Responses output, but the HTTP server does not yet expose `/v1/responses` as a first-class public endpoint.

The implementation must preserve the existing flat `package main` layout, reuse the current routing and auth behavior, and avoid introducing unnecessary dependencies. Because providers differ in native support, the design should allow a single client-facing `/v1/responses` contract while mapping requests onto either native responses transports or existing chat-completions-based provider flows.

## Goals / Non-Goals

**Goals:**
- Expose a public `/v1/responses` endpoint alongside the existing OpenAI-compatible routes.
- Route `/v1/responses` requests through the same model prefix and provider resolution logic used by other endpoints.
- Support both streaming and non-streaming responses with OpenAI-compatible response shapes.
- Reuse existing adapter logic wherever possible so providers that only implement chat completions can still serve `/v1/responses` callers.
- Add regression coverage for request mapping, routing selection, and response normalization.

**Non-Goals:**
- Replacing existing `/v1/chat/completions` behavior or removing older compatibility endpoints.
- Implementing every optional Responses API field on day one if the upstream providers do not support them.
- Introducing a new provider abstraction layer beyond the minimum extension needed to support `/v1/responses`.
- Solving unrelated Codex auth refresh behavior in the same change.

## Decisions

### 1. Add `/v1/responses` as a new proxy entrypoint that delegates into shared routing logic
The server should register `/v1/responses` in the same place as the existing proxied API routes. The request handler should detect this path and invoke a dedicated responses-processing branch rather than forcing all traffic through the chat completions flow.

**Rationale:** This keeps the public API explicit and avoids overloading the chat handler with path-based special cases that become hard to test.

**Alternatives considered:**
- Rewriting `/v1/responses` requests into `/v1/chat/completions` before they reach the proxy. Rejected because it obscures protocol differences, especially streaming event shapes and response object types.
- Creating a separate server module just for Responses API handling. Rejected because the repo intentionally keeps all HTTP logic flat and centralized.

### 2. Use a compatibility adapter that maps `/v1/responses` requests onto provider-specific capabilities
The handler should parse the incoming Responses API payload, resolve the target model/provider, and then choose one of two paths:
- Native responses path for providers that already support Responses API transports.
- Compatibility path that converts the request into the provider’s chat-completions request shape and then adapts the provider response back into a Responses API object.

**Rationale:** This allows incremental provider support while preserving a consistent client-facing contract.

**Alternatives considered:**
- Require every provider to implement native `/v1/responses` before exposing the endpoint. Rejected because it blocks compatibility for providers that already work through chat completions.
- Expose `/v1/responses` only for Codex/OpenAI paths. Rejected because the proxy’s value is unified routing across providers.

### 3. Extend provider interfaces minimally, preferring optional capabilities over mandatory changes
The existing provider abstraction should be extended with an optional responses-specific capability or method rather than changing every provider to implement a new required interface immediately. The proxy can feature-detect support and fall back to compatibility mode where appropriate.

**Rationale:** This limits breakage across providers and makes the change smaller to adopt.

**Alternatives considered:**
- Add a required `ProxyResponsesRequest` method to the base provider interface. Rejected because it forces all providers and tests to change at once.
- Hardcode provider-type branches in the HTTP layer. Rejected because it spreads provider-specific logic outside provider boundaries.

### 4. Preserve existing model-prefix semantics and routing precedence for `/v1/responses`
The Responses endpoint must respect explicit prefixed models such as `oc-*` and `gc-*`, and it must not reintroduce the bug where early Copilot free-model routing overrides explicit model selection.

**Rationale:** `/v1/responses` must behave consistently with the fixed chat routing semantics, or client behavior will diverge by endpoint.

**Alternatives considered:**
- Let the responses path choose its own routing shortcut rules. Rejected because it would create inconsistent provider selection across endpoints.

### 5. Normalize streaming output into OpenAI Responses API event shapes
For streaming mode, the proxy should emit Responses-compatible SSE events even when the upstream provider streams chat-completions deltas. For non-streaming mode, it should emit a single `response` object with normalized output content and metadata.

**Rationale:** Many `/v1/responses` clients depend on the event contract, not just the final text.

**Alternatives considered:**
- Pass upstream SSE through unchanged. Rejected because chat-completions deltas are not wire-compatible with Responses API events.

## Risks / Trade-offs

- **[Protocol mismatch between chat completions and responses]** → Mitigation: explicitly scope the first version to supported fields and reject unsupported combinations with clear 4xx errors.
- **[Streaming adaptation complexity]** → Mitigation: add focused tests for SSE translation and keep transformation logic isolated in adapter helpers.
- **[Provider capability drift]** → Mitigation: use optional capability detection and default to compatibility mode where feasible.
- **[Behavior inconsistency across endpoints]** → Mitigation: reuse the same model parsing and router resolution path already validated for chat completions.
- **[Future Responses API evolution]** → Mitigation: keep adapter logic centralized so new fields and event types can be added without changing the public routing structure.

## Migration Plan

1. Add `/v1/responses` route wiring in the HTTP server.
2. Implement request parsing, provider resolution, and compatibility/native dispatch in the proxy layer.
3. Extend adapter helpers and optional provider capabilities for streaming and non-streaming normalization.
4. Add unit and integration-style tests for routing, request transformation, and response formatting.
5. Verify `make fmt`, `make vet`, `make test`, and `make build` pass before deployment.

Rollback is straightforward: remove or disable the `/v1/responses` route and compatibility handler while leaving existing endpoints untouched.

## Open Questions

- Which subset of optional Responses API fields must be supported immediately versus rejected as unsupported?
- Should providers that support tool calling through chat completions expose tool outputs in the full Responses API item model in the first version, or can the initial implementation flatten some metadata?
- Is there any existing external client in production that depends on exact Responses API streaming event names beyond the common text-delta flow?
