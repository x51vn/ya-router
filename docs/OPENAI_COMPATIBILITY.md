# OpenAI-compatible routed contract

Clients use the OpenAI-style endpoints with `model: "thiendu"`. The router
chooses exactly one configured provider-prefixed target before dispatch and
rewrites upstream response model fields back to `thiendu` for JSON and SSE.
An explicit `github/`, `codex/`, or `kilo/` model bypasses virtual-model
selection. There is no cross-provider replay after dispatch.

| Surface | GitHub Copilot | Codex API-key mode | Codex ChatGPT mode | Kilo Gateway |
|---|---|---|---|---|
| Chat Completions JSON/SSE | supported | supported | supported via Responses translation | supported |
| Native Responses JSON/SSE | not declared; skipped before dispatch | supported | supported | supported |
| Embeddings | supported | supported | not declared; skipped before dispatch | not declared; skipped before dispatch |
| Tool/function fields | forwarded when target supports Chat semantics | translated or forwarded by the Codex adapter | translated or forwarded by the Codex adapter | forwarded by gateway contract |
| Catalog and availability | last-known-good catalog, health/auth aware | last-known-good catalog, health/auth aware | last-known-good catalog, health/auth aware | last-known-good catalog, anonymous/free policy aware |

Provider descriptors advertise their supported capability superset. An active
instance exposes effective capabilities, so a ChatGPT-mode Codex instance does
not advertise embeddings even though API-key Codex does. The selector evaluates
those effective capabilities before dispatching `thiendu`.

The automated suite covers model listing, Chat tool forwarding, Responses
capability selection, JSON/SSE public model identity, no-active-target errors,
selected-target errors without replay, cooldown-based later-request selection,
and the three-provider mock vertical slice. Tests use mock upstreams and no
live credentials. Redacted operator smoke tests for real GitHub Copilot, Codex,
and Kilo remain a release-environment action because they require credentials
and account entitlements that normal CI must not hold.

Unsupported request semantics are not silently dropped by routing: targets
without the endpoint capability are skipped before dispatch. Adapter-level
unsupported fields return typed sanitized errors. Request context cancellation
and timeouts are passed to the one selected upstream request.
