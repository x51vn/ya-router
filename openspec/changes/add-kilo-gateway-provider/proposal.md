# Change: Add Kilo Gateway provider

## Why

Kilo Gateway exposes an OpenAI-compatible API and a stable `kilo-auto/free` tier. ya-router currently cannot route to it, so clients must configure Kilo separately and cannot use the router's deterministic prefixes, health view, credential isolation, or transport policy.

## What changes

- Add a Go `kilo` provider with the authoritative `kilo/` client prefix.
- Discover models from the public Kilo catalog and cache the result.
- Support Chat Completions and native Responses passthrough.
- Support `KILO_API_KEY`, optional organization context, and anonymous free models.
- Restrict anonymous requests to models identified as free.
- Preserve upstream HTTP status and SSE payloads.
- Add CLI/config integration, tests, and operator documentation.

## Out of scope

- Kilo device OAuth.
- Embeddings or FIM endpoints.
- Cross-provider fallback to or from Kilo.
- Any guarantee about the underlying model selected by Auto Free.

