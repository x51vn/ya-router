# MVP1 implementation status

This page is the current implementation-status companion to the historical
architecture and roadmap documents. Runtime code and accepted OpenSpec
requirements remain authoritative when older planning text differs.

## Delivered foundation

- `ya-routerd` owns immutable runtime snapshots, provider lifecycle, revisioned
  configuration, managed secrets, durable operations, and the private Control
  API; `ya-router` remains the compatibility binary.
- `ya` is both a keyboard-driven Bubble Tea dashboard (its default command)
  and a scriptable Control API client.
- GitHub Copilot, Codex, and Kilo descriptors/adapters, redacted credential
  posture, device/API-key/anonymous auth workflows, and revision-safe provider
  enable/disable mutations are implemented.
- `routing.virtual_models` is implemented as a preserved generic engine. The
  default public model is `thiendu`, whose configured priority candidates span
  GitHub Copilot, Codex, and Kilo. Selection is deterministic, capability,
  health, authentication, catalog, allowlist, and cooldown aware, then pinned
  for the request. It never replays an in-flight request across providers.
- Routing diagnostics, Control API status, text/JSON client output, and the
  dashboard expose bounded candidates, selections, skip reasons, catalogs,
  operations, and cooldowns without secrets.
- Linux/amd64 artifact checksums, a systemd unit, and a state-preserving
  single-host deployment guide are available in `docs/LINUX_DEPLOYMENT.md`.

## Current MVP order

1. [#58](https://github.com/duvu/ya-router/issues/58): retain `thiendu` as the
   simple configured public automatic-routing model.
2. [#60](https://github.com/duvu/ya-router/issues/60): lock OpenAI-compatible
   routed behavior and its three-provider compatibility matrix.
3. [#62](https://github.com/duvu/ya-router/issues/62): retain later-request
   cooldown feedback without cross-provider replay.
4. [#13](https://github.com/duvu/ya-router/issues/13),
   [#14](https://github.com/duvu/ya-router/issues/14),
   [#31](https://github.com/duvu/ya-router/issues/31),
   [#16](https://github.com/duvu/ya-router/issues/16),
   [#17](https://github.com/duvu/ya-router/issues/17), and
   [#18](https://github.com/duvu/ya-router/issues/18): daemon auth, safe
   mutations, routing status, TUI, and Linux deployment vertical slice.
5. [#61](https://github.com/duvu/ya-router/issues/61): provider conformance and
   onboarding baseline.
6. [#59](https://github.com/duvu/ya-router/issues/59): final mock and redacted
   real-provider MVP walkthrough.

## Deliberately deferred

[#19](https://github.com/duvu/ya-router/issues/19) remains post-MVP hardening:
full sustained-load/fault testing, broader release matrices, SBOM/signing, and
advanced deployment hardening. The MVP dashboard does not expose generic
routing-policy CRUD, account CRUD, systemd/Docker lifecycle control, OIDC, or
persisted named client profiles. Those limitations do not remove the generic
router, provider adapters, selectors, diagnostics, metrics, or existing tests.
