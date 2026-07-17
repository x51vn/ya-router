# Umbrella Model Routing Delivery Roadmap

Status: historical delivery roadmap; the generic routing foundation is implemented
Architecture: [`docs/architecture/umbrella-model-routing.md`](../architecture/umbrella-model-routing.md)  
Epic: [#25](https://github.com/duvu/ya-router/issues/25)  
Last updated: 2026-07-15

## 1. Outcome

The delivered foundation lets a client send one stable virtual model ID and
`ya-router` chooses the first currently active canonical provider/model target.
`thiendu` is the configured MVP public ID; generic `routing.virtual_models`
remains supported.

The feature must preserve one strict invariant:

> One request is selected once and dispatched once. An error from the selected target is returned; the request is never retried against another provider/model.

## 2. Delivery principles

1. **Selection is routing, not retry.** Choose before dispatch and do not add cross-provider logic to proxy/provider error paths.
2. **Explicit routes remain absolute.** `model_map` and provider prefixes keep their current meaning.
3. **Start with one understandable strategy.** V1 supports ordered priority only.
4. **No network I/O on the selection hot path.** Selection reads atomic last-known-good availability state.
5. **Static config first.** Core routing can ship before Control API/TUI mutation support.
6. **Managed mutation stays daemon-owned.** Later UI/CLI writes use revisioned control resources.
7. **No secret-bearing diagnostics.** Routing state uses stable bounded reason codes.
8. **Every slice preserves Go race-test and data-plane compatibility gates.**

## 3. Dependency graph

```text
#26 Contract and schema
    |
    v
#27 Availability snapshots
    |
    v
#28 Priority selector
    |
    v
#29 Data-plane integration
    |
    +------------------+
    v                  v
#30 Observability    #32 Core production gates
    |                  ^
    +------------------+

#29 + YA-TUI #11/#14/#15/#17
    |
    v
#31 Control API / CLI / TUI
    |
    v
#32 Full managed-service acceptance
```

## 4. Milestones

### M0 — Architecture and backlog (completed)

Deliverables:

- accepted architecture;
- accepted routing precedence and no-failover invariant;
- GitHub epic and ordered issues;
- README and wiki-source documentation.

Issues: #25–#32 planning baseline.

Exit criteria:

- maintainers agree that umbrella routing is exact pre-dispatch selection;
- V1 policy remains priority-only with prefixed, non-nested targets;
- docs distinguish delivered routing behavior from remaining MVP conformance
  and post-MVP hardening work.

### M1 — Configuration and availability foundation (completed)

Issues: #26 and #27.

Deliverables:

- backward-compatible virtual-model schema;
- deep-cloned immutable config;
- validation before publication;
- atomic provider/model availability view;
- last-known-good catalog freshness and stable rejection reasons.

Exit criteria:

- existing configs load unchanged;
- invalid policies cannot reach request routing;
- selectors can inspect availability without network calls or provider mutation;
- race tests cover refresh/read publication.

### M2 — Core selector (completed)

Issue: #28.

Deliverables:

- pure deterministic priority selector;
- bounded decision metadata;
- typed no-active-target outcome;
- table, fuzz, and benchmark coverage.

Exit criteria:

- same inputs always select the same target;
- selection returns at most one target;
- selector performs no I/O and contains no provider implementation knowledge.

### M3 — Usable static-config data plane (completed)

Issue: #29.

Deliverables:

- exact virtual-model routing branch;
- selected target resolved through existing prefix routing;
- request-body model rewrite;
- provider/model pinning under runtime lease;
- virtual models visible in `/v1/models`.

Exit criteria:

- a client can call `thiendu` from Chat Completions or Responses when target capabilities permit;
- an upstream failure results in one provider invocation only;
- a later request may select a different target after availability changes;
- all existing routing behavior remains green.

This is the first independently useful release slice.

### M4 — Operability (completed)

Issue: #30.

Deliverables:

- structured routing-decision logs;
- bounded metrics;
- redacted virtual-model readiness diagnostics;
- operator troubleshooting documentation.

Exit criteria:

- operators can explain why one target was selected or skipped;
- diagnostics clearly state that no cross-provider failover occurred;
- metric cardinality is bounded by configuration.

### M5 — Managed configuration and TUI (completed MVP subset)

Issue: #31.

Depends on managed-service issues #11, #14, #15, and #17.

Deliverables:

- versioned read resources;
- revision-safe validate/diff/apply/rollback;
- `ya` text/JSON commands;
- read-only then mutation TUI workflows.

Exit criteria:

- the daemon remains the only writer;
- two clients cannot overwrite target order silently;
- failed apply leaves the old effective runtime active;
- dry run performs no upstream inference request.

### M6 — MVP acceptance evidence

Issue: #32.

Deliverables:

- concurrent data-plane and availability/config mutation tests;
- fault and request-duplication tests;
- migration/rollback evidence;
- rollout and incident runbook;
- release-readiness report.

Exit criteria:

- one request is never delivered to more than one umbrella target;
- no P0/P1 correctness or secret-handling defect remains;
- static-config and managed-control acceptance evidence are linked;
- existing provider/routing/SSE/error compatibility remains blocking.

## 5. Ordered implementation backlog

### [YA-MR-01 — #26](https://github.com/duvu/ya-router/issues/26)

Define the configuration and domain contract.

Primary output:

- optional `routing.virtual_models` schema;
- V1 priority strategy;
- canonical target and collision validation;
- deep clone and backward-compatibility tests.

### [YA-MR-02 — #27](https://github.com/duvu/ya-router/issues/27)

Build atomic target availability snapshots.

Primary output:

- provider/capability/health/catalog state;
- last-known-good freshness;
- stable target rejection reasons;
- race-safe publication.

### [YA-MR-03 — #28](https://github.com/duvu/ya-router/issues/28)

Implement the deterministic selector.

Primary output:

- pure priority selection;
- one selected target or typed no-target error;
- bounded redacted decision metadata.

### [YA-MR-04 — #29](https://github.com/duvu/ya-router/issues/29)

Integrate with the data plane and model catalog.

Primary output:

- updated resolution order;
- selected target pinning and body rewrite;
- `/v1/models` virtual entries;
- tests proving zero cross-provider retry.

### [YA-MR-05 — #30](https://github.com/duvu/ya-router/issues/30)

Add observability and diagnostics.

Primary output:

- selection/skipped-target logs;
- bounded metrics;
- readiness summary and troubleshooting docs.

### [YA-MR-06 — #31](https://github.com/duvu/ya-router/issues/31)

Add Control API, CLI, and TUI workflows.

Primary output:

- redacted read resources;
- revision-safe mutations;
- scriptable JSON commands;
- safe reorder/edit TUI.

### [YA-MR-07 — #32](https://github.com/duvu/ya-router/issues/32)

Complete production and regression gates.

Primary output:

- E2E, race, load, fault, and duplication evidence;
- upgrade/rollback and runbooks;
- release-readiness decision.

## 6. Parallelization guidance

Safe parallel work:

- documentation and test-fixture design while #26 is implemented;
- availability read-model internals after #26 domain types stabilize;
- observability schema design while #29 integration is underway;
- Control API resource design after #29 output contracts stabilize and YA-TUI read APIs exist.

Unsafe parallel work:

- selector implementation before active/reason semantics are stable;
- proxy failover experiments while the umbrella feature is being built;
- TUI mutation before revisioned daemon config mutation exists;
- adding weighted/adaptive policies before priority behavior is production-proven;
- changing existing prefix/model-map precedence in the same implementation PR.

## 7. Release strategy

### Preview A — static configuration

Includes #26–#29.

- Operator configures `virtual_models` in JSON.
- `/v1/models` shows umbrella IDs.
- Selection is deterministic and no-failover.
- Suitable for local/trusted pilot use after core gates pass.

### Preview B — observable routing

Adds #30.

- Selection decisions and no-target reasons are diagnosable.
- Operational dashboards/runbooks can be validated.

### Release candidate — managed workflows

Adds #31 after the relevant YA-TUI dependencies.

- Daemon-owned validate/diff/apply/rollback.
- Scriptable client and TUI workflows.

### Production

Completes #32.

- Concurrency, failure, duplication, compatibility, migration, and rollback evidence accepted.

## 8. Rollout and rollback

1. Deploy code with no virtual models configured; verify complete behavioral parity.
2. Configure one non-critical umbrella ID with one target; this should behave as a stable alias.
3. Add a second lower-priority target and verify selection diagnostics under controlled health changes.
4. Observe no-target and selected-target error behavior.
5. Enable managed mutation only after revisioned Control API support is accepted.

Rollback removes the virtual-model configuration or restores the prior config revision. Existing explicit provider-prefixed client IDs remain available throughout.

## 9. Definition of done for each issue

- Scope and non-goals remain bounded.
- No post-dispatch cross-provider path is introduced.
- Tests include race coverage where mutable observations are involved.
- Existing provider prefix/model-map and protocol tests remain blocking.
- Error and diagnostic values are typed, bounded, and redacted.
- README, architecture, roadmap, configuration, and wiki-source docs are updated when behavior becomes available.
- PR body links acceptance evidence and states upgrade/rollback impact.
