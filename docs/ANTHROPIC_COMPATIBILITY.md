# Anthropic and Claude Code compatibility

`ya-router` provides an Anthropic Messages facade for Claude Code while keeping
the existing OpenAI-compatible data plane unchanged. It is an adapter to the
existing native Responses capability, not an Anthropic model emulator.

## Connect Claude Code

Configure an explicit alias in `routing.claude_aliases`, enable and authenticate
the target provider, then point Claude Code at the local listener:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:7071
export ANTHROPIC_API_KEY="$YA_ROUTER_API_KEY"
claude
```

When the listener is loopback-only and `YA_ROUTER_API_KEY` is unset,
`ANTHROPIC_API_KEY` may contain any non-empty local value. For a non-loopback
listener, it must match `YA_ROUTER_API_KEY`. Claude Code may probe `HEAD /`.

## Supported gateway surface

- `POST /v1/messages`, including `?beta=true`;
- `Authorization: Bearer` and `x-api-key` gateway authentication;
- text and base64 image content blocks;
- system text blocks, message text, function tools, `tool_use`, and
  `tool_result` continuation;
- tool choice, `max_tokens`, temperature, top-p, adaptive-thinking consumption,
  explicit reasoning effort, and JSON-schema output format;
- non-streaming Message responses and incremental Anthropic SSE events for
  text and partial tool-input JSON;
- `GET /v1/models?limit=1000` alias discovery and manual alias pinning.

Every request selects one native Responses-capable route before dispatch. A
selected request is never replayed to another provider. The external message
model remains the requested Claude alias even when its canonical target is a
Codex or another non-Claude provider.

## Explicit limits

`POST /v1/messages/count_tokens` is not implemented. Unknown body fields,
unknown content blocks, unsupported thinking modes, unsupported output formats,
tool-result error flags, malformed JSON, and oversized requests or tool schemas
fail before upstream delivery. The request limit is 5 MiB and a tool schema is
limited to 256 KiB.

The adapter does not fabricate thinking blocks, thinking signatures, or hidden
reasoning. All Responses-capable targets accept `low`, `medium`, and `high`.
`xhigh` and `max` are accepted only for `codex/gpt-5*` targets; other selected
models fail before dispatch. Set `CLAUDE_CODE_ATTRIBUTION_HEADER=0` when an
installation does not want Claude Code attribution material added to its own
system context.

## Security and protocol evolution

Gateway credentials, `anthropic-*`, and `x-claude-code-*` headers are consumed
at the gateway and are never forwarded to a provider. `Idempotency-Key` is the
only client header forwarded to preserve the existing unsafe-request retry
contract. Future Anthropic and Claude Code header names are therefore accepted
without a brittle allowlist while still remaining server-local.

Errors use the Anthropic error envelope and retain gateway-safe authentication,
permission, rate-limit, and availability status. `Retry-After` and safe request
ID headers are preserved. Request content, tool arguments, credentials,
thinking content, and response content are excluded from default gateway logs
and diagnostics.

## Compatibility matrix

| Target | Text | Streaming | Tools | Structured output | Embeddings |
|---|---:|---:|---:|---:|---:|
| Codex Responses | yes | yes | yes | yes | not applicable |
| Kilo Responses | capability-dependent | capability-dependent | capability-dependent | capability-dependent | not applicable |
| GitHub Copilot | rejected before dispatch | rejected before dispatch | rejected before dispatch | rejected before dispatch | not applicable |

Normal CI uses mock providers and fuzz seeds only. Credential-dependent Claude
Code and real-provider checks remain release-environment evidence and are
waived for the requested issue closure.
