# Umbrella Model Routing Architecture

Status: implemented generic routing foundation; current MVP contract uses `thiendu`
Epic: [#25 — Umbrella models with active-target routing](https://github.com/duvu/ya-router/issues/25)  
Last updated: 2026-07-15

## 1. Decision summary

`ya-router` supports client-facing **umbrella models** (also called virtual
models). `thiendu` is the default simple public model; the generic
`routing.virtual_models` engine remains supported for additional configured IDs.

An umbrella model is not an upstream model and is not a failover chain. It is an ordered policy that selects one canonical provider-prefixed target before a request is dispatched:

```text
thiendu
  1. github/gpt-5-mini
  2. codex/gpt-5.4-mini
  3. kilo/kilo-auto/free
```

For each request, the router:

1. reads one immutable runtime and availability snapshot;
2. evaluates targets in configured order;
3. selects the first target that is active for the requested capability;
4. resolves that target through the existing provider-prefix route;
5. pins the selected provider/model for the full request;
6. forwards the selected target's result or error unchanged through existing provider semantics.

The same request never moves to another provider/model after dispatch. A later request may select another target if health or catalog state has changed.

## 2. Why this is a separate routing concept

The current router has deterministic route mechanisms:

- `routing.model_map` for explicit aliases;
- authoritative provider prefixes (`github/`, `codex/`, `kilo/`);
- provider catalog discovery for ordinary bare IDs;
- a default provider only when the request omits a model.

A single `model_map` entry cannot express “choose the first currently active target from this ordered set.” Adding retries to the proxy layer would be incorrect because it could duplicate non-idempotent model requests and would weaken provider isolation.

Umbrella routing therefore belongs in the routing decision phase, before any provider call.

## 3. Terminology

| Term | Meaning |
|---|---|
| Umbrella model | Client-facing virtual model configured under `routing.virtual_models` |
| Target | Canonical provider-prefixed model ID referenced by an umbrella model |
| Active/routable | Eligible for selection for one capability under the current availability snapshot |
| Selection | Choosing exactly one target before dispatch |
| Request pinning | Retaining the selected target and provider instance for the entire request |
| Provider-internal retry | Account/token/transport behavior owned by a provider implementation |
| Cross-provider failover | Retrying the same request against a different provider/model; explicitly not supported |

## 4. Goals

1. Give clients one stable model ID while operators control an ordered target list.
2. Select only targets that are currently usable for the request capability.
3. Keep behavior deterministic and easy to explain.
4. Preserve all explicit routing and provider isolation guarantees.
5. Avoid duplicate upstream generations caused by post-dispatch failover.
6. Make every decision observable using bounded, redacted metadata.
7. Preserve static configuration and expose read-only readiness through the
   daemon-owned Control API and TUI; generic routing-policy CRUD remains out of
   MVP scope.

## 5. Non-goals for v1

- Retry or fail over to another target after dispatch.
- Weighted, random, round-robin, cost, latency, or quality-based selection.
- Prompt inspection or LLM-based model choice.
- Umbrella models referencing other umbrella models.
- Automatic “best model” inference.
- Changing provider-internal account behavior.
- Creating another configuration writer outside `ya-routerd`.

## 6. Configuration contract

Implemented backward-compatible shape:

```json
{
  "routing": {
    "virtual_models": {
      "thiendu": {
        "strategy": "priority",
        "targets": [
          "github/gpt-5-mini",
          "codex/gpt-5.4-mini",
          "kilo/kilo-auto/free"
        ]
      }
    }
  }
}
```

### 6.1 V1 restrictions

- `priority` is the only strategy.
- List order is priority order.
- Every target must use a known provider prefix.
- Targets must be unique within one umbrella model.
- At least one target is required.
- A target cannot reference an umbrella model.
- An umbrella ID cannot shadow a provider-prefixed ID or an explicit `model_map` key.
- The `router/` namespace is reserved for router-owned client model IDs.

These restrictions keep validation local and prevent ambiguity, recursion, and hidden provider selection.

## 7. Routing precedence

The target resolution order becomes:

1. exact `routing.model_map` entry;
2. explicit provider prefix;
3. exact configured umbrella model ID;
4. provider catalog discovery for ordinary bare model IDs;
5. configured default provider only when the request omitted a model.

Consequences:

- `github/*`, `codex/*`, and `kilo/*` never enter umbrella selection;
- existing aliases continue to win over virtual models;
- an unknown explicit model remains an error;
- only exact configured umbrella IDs trigger selection.

## 8. Definition of active

A target is active for a request capability only when all conditions are satisfied:

1. The target prefix identifies a provider present in the request's immutable runtime snapshot.
2. The provider supports the requested capability.
3. The provider health snapshot is `ready`.
4. The last-known-good model catalog includes the target's bare model ID.
5. The catalog state is accepted by the configured freshness policy.
6. Existing provider allowlist, entitlement, and anonymous/free rules permit the target.
7. The target is not disabled by configuration.

The first release is conservative: degraded providers and unacceptable stale catalogs are skipped unless a later accepted configuration contract explicitly permits them.

## 9. Availability snapshots

Umbrella selection must not trigger network calls. The request hot path reads an immutable or atomically published availability view containing:

- provider ID and runtime generation;
- effective capabilities;
- redacted health state and observation timestamp;
- last-known-good model IDs;
- catalog fetched timestamp and stale state;
- stable target rejection reason codes.

Catalog refresh remains provider/cache owned. A refresh failure retains last-known-good data but marks its freshness state. Readers never observe a partially refreshed catalog.

Recommended reason codes:

```text
provider_not_registered
provider_not_ready
capability_unsupported
model_not_in_catalog
catalog_stale
model_disallowed
target_disabled
```

Reason strings from upstream providers are not copied into routing state.

## 10. Selection algorithm

The v1 selector is a pure function:

```text
select(virtual model, capability, availability snapshot)
  for target in configured order:
    if target is active:
      return target and bounded decision metadata
  return no_active_target
```

The result includes:

- umbrella model ID;
- selected target ID;
- target index;
- strategy;
- runtime/catalog generation or timestamps;
- skipped target IDs with stable reason codes.

For the same inputs, the output is deterministic.

## 11. Request pinning and no-failover invariant

After selection, the selected canonical target is resolved using the existing prefix path. The router returns one provider instance and one bare upstream model.

The request body is patched once, and the request continues under the runtime lease acquired at handler entry.

If the selected target returns any of the following, the response follows existing provider behavior and no other umbrella target is attempted:

- `401` authentication failure;
- `403` entitlement failure;
- `429` rate limit;
- `5xx` upstream failure;
- timeout or cancellation;
- transport error;
- malformed upstream response.

This is the principal safety boundary of the feature. It prevents accidental duplicate generations and keeps provider-specific retry policy inside providers.

Provider-internal account handling is separate. Umbrella routing neither expands nor disables that behavior.

## 12. Model catalog contract

`GET /v1/models` will expose each configured umbrella model once, using router ownership metadata:

```json
{
  "id": "thiendu",
  "object": "model",
  "owned_by": "ya-router"
}
```

The catalog should not claim that the umbrella ID is a specific upstream model. Additional redacted metadata may be added only if it remains OpenAI-client compatible.

Provider target models remain independently visible with their normal provider prefixes.

## 13. Error contract

A configured umbrella with no active target returns a typed routing/model-unavailable error. Diagnostic details are bounded to configured targets and stable reason codes.

Example conceptual payload:

```json
{
  "error": {
    "type": "model_unavailable",
    "message": "No active target is available for model thiendu."
  }
}
```

Public data-plane errors do not expose secrets, raw account IDs, arbitrary upstream bodies, or internal provider objects.

## 14. Observability

Structured decision logs should include:

- requested umbrella ID;
- selected target ID;
- strategy and target index;
- runtime/catalog generation;
- skipped reason codes;
- final provider and final HTTP status.

Metrics should cover:

- selections by umbrella and target;
- no-active-target outcomes;
- skipped target reasons;
- stale catalog decisions;
- selected-target upstream outcomes.

Logs and metrics must distinguish selection from provider-internal retry. They must never imply that cross-provider failover occurred.

Prompts, completions, tokens, keys, device codes, arbitrary upstream bodies, and raw account IDs are excluded.

## 15. Control API and TUI integration

Static config support can ship before the managed control plane is complete.

The daemon single-writer, revisioned config, read resources, client SDK, and
mutation workflows expose:

- listing umbrella models and current redacted readiness;
- validating and dry-running a policy without sending a model request;
- creating, updating, deleting, and reordering targets;
- revision conflict detection, idempotency, audit, and rollback;
- dashboard readiness and provider enable/disable/auth workflows. Generic
  virtual-model policy CRUD remains intentionally deferred.

The `ya` client and TUI never edit the service JSON directly.

## 16. Security and reliability requirements

- Selection performs no network I/O and no provider mutation.
- Runtime and availability views are immutable or atomically published.
- Config validation rejects ambiguity before publication.
- Provider replacement/removal produces generation-consistent availability.
- Slow or failed catalog refresh does not hold routing locks indefinitely.
- Diagnostic arrays are bounded by configured target count.
- Metrics labels are bounded to configured umbrella/target IDs.
- A failure during config application leaves the previous effective runtime active.

## 17. Test strategy

Blocking tests include:

1. Config validation and deep clone tests.
2. Availability reason-code and last-known-good catalog tests.
3. Deterministic selector table and fuzz tests.
4. Routing precedence and body-rewrite integration tests.
5. Streaming and non-streaming request pinning tests.
6. Mock-provider tests proving exactly one upstream receives a failed umbrella request.
7. Concurrent health/catalog refresh and routing race tests.
8. Provider replace/remove tests under active request leases.
9. Existing prefix, model-map, bare discovery, default route, SSE, status, and provider error regression tests.
10. Redaction and bounded-cardinality tests.

## 18. Delivery backlog

| Order | Issue | Outcome | Status |
|---:|---|---|---|
| 1 | [#26](https://github.com/duvu/ya-router/issues/26) | Contract and configuration schema | Delivered |
| 2 | [#27](https://github.com/duvu/ya-router/issues/27) | Target availability snapshots | Delivered |
| 3 | [#28](https://github.com/duvu/ya-router/issues/28) | Deterministic priority selector | Delivered |
| 4 | [#29](https://github.com/duvu/ya-router/issues/29) | Data-plane and model-catalog integration | Delivered |
| 5 | [#30](https://github.com/duvu/ya-router/issues/30) | Observability and diagnostics | Delivered |
| 6 | [#31](https://github.com/duvu/ya-router/issues/31) | Control API, CLI, and TUI integration | Delivered for bounded routing status and dashboard observation |
| 7 | [#32](https://github.com/duvu/ya-router/issues/32) | Production and regression gates | Superseded for MVP1 by [#59](https://github.com/duvu/ya-router/issues/59) end-to-end gate |

### Implementation map (Go runtime)

| Concern | Location |
|---|---|
| Config schema + validation | `internal/config` (`types.go`, `clone.go`, `validate.go`) |
| Availability contract | `internal/availability` |
| Priority selector | `internal/routing/selector.go` |
| Router integration + no-failover | `internal/routing/router.go` |
| Diagnostics + metrics | `internal/routing/diagnostics.go`, `internal/routing/metrics.go` |
| `/v1/models` exposure + decision logs | `src/models.go`, `src/umbrella_observability.go` |
| Health diagnostics endpoint | `src/managed_runtime.go` (`/health/umbrella`) |

## 19. Architecture decisions

| ID | Decision | Rationale |
|---|---|---|
| MR-AD-01 | Select before dispatch | Prevents duplicated requests and keeps routing separate from transport retries |
| MR-AD-02 | No cross-provider failover | Preserves deterministic behavior and provider isolation |
| MR-AD-03 | Priority-only strategy in v1 | Simple, testable, and operator-readable |
| MR-AD-04 | Canonical prefixed targets only | Reuses existing routing guarantees and prevents ambiguity |
| MR-AD-05 | No nested umbrella models | Avoids cycles and opaque decision chains |
| MR-AD-06 | Atomic availability read model | Keeps request selection fast and race-safe |
| MR-AD-07 | Static config before managed mutation | Delivers core value without bypassing daemon single-writer architecture |
