## Why

The service already exposes OpenAI-compatible endpoints for models, chat completions, and embeddings, but clients that use the newer `/v1/responses` API cannot use this proxy directly. Supporting `/v1/responses` now allows newer OpenAI/Codex-compatible tools to work against the service without forcing clients to downgrade to `/v1/chat/completions`.

## What Changes

- Add OpenAI-compatible `/v1/responses` request handling to the HTTP server.
- Route `/v1/responses` requests through the existing provider and model routing system.
- Reuse or extend the existing response adaptation layer so providers that only support chat completions can still serve `/v1/responses` callers.
- Normalize streaming and non-streaming responses into the expected OpenAI Responses API shape.
- Add tests covering request transformation, routing behavior, and response formatting for supported providers.

## Capabilities

### New Capabilities
- `responses-api`: Support OpenAI-compatible `/v1/responses` requests across routed models and providers.

### Modified Capabilities
- None.

## Impact

- HTTP request handling in `src/server.go` and/or `src/proxy.go`.
- Request/response transformation logic in `src/responses_adapter.go` and related proxy helpers.
- Provider routing behavior in `src/router.go`, `src/provider.go`, and provider implementations as needed.
- New regression and compatibility coverage in `src/*_test.go`.
- User-facing API compatibility for clients expecting the OpenAI Responses API.
