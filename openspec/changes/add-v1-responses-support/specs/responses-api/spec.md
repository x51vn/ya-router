## ADDED Requirements

### Requirement: Clients can call `/v1/responses`
The service SHALL expose an OpenAI-compatible `/v1/responses` endpoint that accepts POST requests for supported models.

#### Scenario: Request is sent to responses endpoint
- **WHEN** a client sends a valid POST request to `/v1/responses`
- **THEN** the service routes the request through the proxy instead of returning a missing-route error

### Requirement: Responses requests follow existing model routing rules
The service SHALL resolve the model for `/v1/responses` requests using the same explicit-prefix and provider-routing rules used by the existing proxied endpoints.

#### Scenario: Explicit provider prefix is preserved
- **WHEN** a client sends `/v1/responses` with a model that includes an explicit provider prefix
- **THEN** the service routes the request to that provider and SHALL NOT override it with an unrelated provider shortcut

### Requirement: Compatibility mode supports providers without native responses transport
The service SHALL serve `/v1/responses` for providers that only support chat-completions-style upstream requests by transforming the client request into the provider-supported request format and adapting the result back into a Responses API object.

#### Scenario: Provider only supports chat completions
- **WHEN** a resolved provider does not expose a native responses transport
- **THEN** the service transforms the request into the provider-supported format and returns an OpenAI-compatible Responses API result

### Requirement: Non-streaming responses use Responses API output format
The service SHALL return non-streaming `/v1/responses` results as OpenAI-compatible response objects with normalized output content.

#### Scenario: Non-streaming request succeeds
- **WHEN** a client sends `/v1/responses` with streaming disabled and the upstream call succeeds
- **THEN** the service returns a successful JSON response shaped as a Responses API object

### Requirement: Streaming responses use Responses API event format
The service SHALL emit Server-Sent Events for streaming `/v1/responses` requests using Responses API-compatible event types and termination behavior.

#### Scenario: Streaming request succeeds
- **WHEN** a client sends `/v1/responses` with streaming enabled and the upstream call succeeds
- **THEN** the service returns Responses API-compatible SSE events and terminates the stream correctly

### Requirement: Unsupported responses features fail explicitly
The service SHALL reject unsupported `/v1/responses` request combinations with a clear client error instead of silently dropping critical behavior.

#### Scenario: Unsupported field combination is requested
- **WHEN** a client sends a `/v1/responses` payload that requires unsupported behavior
- **THEN** the service returns a 4xx error explaining that the requested feature is not supported
