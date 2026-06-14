## Context

`github-copilot-svcs` now supports native `/v1/responses` proxying for Codex-routed models by translating OpenAI-compatible client payloads into ChatGPT Codex upstream requests. Production traffic shows that the ChatGPT Codex backend rejects requests unless `store` is explicitly set to `false`, returning `{"detail":"Store must be set to false"}` before any model work happens. The adapter currently models only a subset of Responses request fields, so omitted `store` values are not normalized and explicit client values are not constrained before upstream dispatch.

## Goals / Non-Goals

**Goals:**
- Ensure every native Codex responses upstream request includes `store: false`.
- Keep the public `/v1/responses` contract compatible for clients that omit `store`.
- Add focused regression coverage around request normalization so future adapter refactors do not reintroduce the failure.

**Non-Goals:**
- Redesign all Responses request validation rules.
- Change non-Codex provider behavior beyond what is needed to preserve existing compatibility.
- Broaden this change into the separate `reasoningSummary` fallback issue.

## Decisions

1. Normalize `store` inside the Responses adapter layer before native Codex request marshaling.
   - Rationale: `responses_adapter.go` already owns OpenAI-compatible request parsing and upstream body shaping, making it the narrowest place to enforce Codex-specific defaults without scattering request mutations across proxy and provider code.
   - Alternative considered: patch the outbound JSON inside `codex_provider.go` after marshaling. Rejected because it duplicates request knowledge and makes tests less direct.

2. Treat any missing or truthy client `store` value as `false` for Codex native responses requests.
   - Rationale: the observed upstream contract is stricter than the public client shape, so the proxy should normalize to the only accepted value rather than surfacing a backend-specific failure for an otherwise valid request.
   - Alternative considered: reject client requests that specify `store=true`. Rejected for this fix because the immediate production need is successful interop, and normalization is simpler and safer.

3. Cover both adapter-level request building and proxy/provider integration with regression tests.
   - Rationale: adapter tests verify exact request shaping, while proxy/provider tests ensure the Codex path continues to use the normalized body during real routing.
   - Alternative considered: rely on only one test layer. Rejected because this bug crosses both translation and provider dispatch boundaries.

## Risks / Trade-offs

- [Client intent around `store=true` is silently ignored] → Document the normalization in the spec and keep the behavior isolated to Codex native responses, where upstream only accepts `false`.
- [Future upstream contract may evolve] → Centralize the rule in the adapter and cover it with tests so adjustments are localized if Codex later accepts different values.
- [Related unsupported fields could mask further backend issues] → Keep this change narrowly scoped and validate production again before batching additional request-shaping changes.

## Migration Plan

1. Update Responses request modeling and native Codex request builders to force `store: false`.
2. Add regression tests for adapter output and routed Codex requests.
3. Run `make fmt && make vet && make test && make build`.
4. Deploy the updated image using the existing `server2` workflow and verify `/v1/responses` no longer returns the upstream store error.
5. Roll back by restoring the previous image tag in `~/deployment/server2/docker-compose.yml` if unexpected regressions appear.

## Open Questions

- Should future compatibility work also normalize `store=false` in non-native fallback paths if another upstream starts enforcing a similar contract?
- Should the proxy emit a debug log when client `store` is overridden, or is silent normalization sufficient for now?
