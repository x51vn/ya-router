## 1. Route and request handling

- [x] 1.1 Register `/v1/responses` in the HTTP server alongside the existing proxied API routes
- [x] 1.2 Add a dedicated `/v1/responses` branch in the proxy request flow that parses the request body and resolves the target model/provider
- [x] 1.3 Ensure `/v1/responses` preserves explicit provider-prefix routing semantics and does not trigger unrelated shortcut routing

## 2. Provider dispatch and compatibility adaptation

- [x] 2.1 Define an optional provider capability for native responses handling or equivalent feature detection
- [x] 2.2 Implement compatibility dispatch that converts `/v1/responses` requests into provider-supported chat-completions requests when native responses transport is unavailable
- [x] 2.3 Normalize non-streaming upstream results into OpenAI-compatible Responses API JSON objects
- [x] 2.4 Normalize streaming upstream results into Responses API-compatible SSE events and end-of-stream behavior
- [x] 2.5 Reject unsupported `/v1/responses` field combinations with clear 4xx errors

## 3. Verification

- [x] 3.1 Add unit tests for `/v1/responses` request transformation and unsupported-field validation
- [x] 3.2 Add routing tests proving explicit prefixed models follow the correct provider path for `/v1/responses`
- [x] 3.3 Add streaming and non-streaming response formatting tests for the responses adapter path
- [x] 3.4 Run `make fmt && make vet && make test && make build` and confirm the full verification sequence passes
