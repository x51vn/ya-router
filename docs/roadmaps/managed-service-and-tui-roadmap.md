# Managed Service and TUI Delivery Roadmap

Status: proposed execution roadmap  
Architecture contract: [`docs/architecture/managed-service-and-tui.md`](../architecture/managed-service-and-tui.md)  
Epic: [#20 — Managed ya-router service, Control API, and TUI](https://github.com/duvu/ya-router/issues/20)
Last updated: 2026-07-15

## 1. Outcome

Deliver a production-capable ya-router distribution consisting of:

- `ya-routerd`, deployable through systemd or Docker;
- `ya`, an OS client with an interactive TUI and scriptable control commands;
- a separate, versioned, authenticated control API;
- safe runtime provider/auth/config management without data-plane interruption;
- production packaging, release, security, observability, and compatibility evidence.

The work is deliberately incremental. The existing Go binary and `/v1/*` contract remain deployable after every phase.

## 2. Delivery principles

1. **Control plane after ownership:** make the daemon the only state writer before accepting mutations from a remote client.
2. **Read before write:** ship and validate a read-only control API/TUI before adding auth and config mutation.
3. **No secret round-trip:** clients may submit secrets but never read them back.
4. **Data plane remains available:** management changes must not interrupt unrelated requests.
5. **One contract, several deployments:** systemd, Docker, TUI, and automation use the same control model.
6. **Small accepted slices:** every issue must have independent acceptance criteria and rollback behavior.
7. **No hidden migration:** config and credential schema changes require versioning, backups, and rollback evidence.
8. **No combined Rust cutover:** Go remains the reference implementation while this roadmap is delivered.

## 3. Priority model

| Priority | Meaning |
|---|---|
| P0 | Architectural/correctness dependency; later work must not start without it |
| P1 | Required for the first usable managed-service/TUI release |
| P2 | Required for production acceptance and supported distribution |
| P3 | Follow-up capability that may ship after the first production release |

## 4. Dependency overview

```text
YA-TUI-01 Package boundaries
    |
    v
YA-TUI-02 Runtime/provider manager
    |
    +--------------------+
    v                    v
YA-TUI-03 State/config   YA-TUI-04 Control API/security
    |                    |
    +----------+---------+
               v
        YA-TUI-05 Read-only API
               |
        +------+------+
        v             v
YA-TUI-06 Operations  YA-TUI-09 Client SDK/ctl
        |             |
        v             v
YA-TUI-07 Auth        YA-TUI-10 Read-only TUI
        |             |
        +------+------+
               v
      YA-TUI-08 Mutations/hot reload
               |
               v
      YA-TUI-11 Operational TUI
               |
        +------+------+
        v             v
YA-TUI-12 Packaging  YA-TUI-13 Production gates
        |             |
        +------+------+
               v
          Production release
```

Where parallel work is shown, shared API/domain types must be merged before downstream branches diverge.

## 5. Milestones

### Milestone M0: Planning baseline

Deliverables:

- accepted architecture and roadmap;
- accepted OpenSpec requirements;
- GitHub epic and ordered child backlog;
- no runtime behavior change.

Exit criteria:

- maintainers agree on binary names, control/data boundary, daemon single-writer rule, and MVP scope;
- every roadmap issue links to this architecture and has bounded acceptance criteria.

### Milestone M1: Managed daemon foundation

Issues: YA-TUI-01 through YA-TUI-03.  
Target: the current service can be refactored internally without changing external behavior.

Exit criteria:

- package boundaries support two binaries;
- provider registry no longer relies on global construction side effects;
- runtime snapshots can be replaced safely under race-enabled load tests;
- daemon exclusively owns config writes while running;
- configuration revision, locking, recovery, and restart-required semantics exist;
- existing `/v1/*`, auth, model, and config compatibility tests remain green.

### Milestone M2: Read-only control plane

Issues: YA-TUI-04, YA-TUI-05, YA-TUI-09, and YA-TUI-10.  
Target: operators can connect safely and inspect service/provider/model state.

Exit criteria:

- local Unix-socket control API is enabled by default for managed installations;
- data-plane credentials cannot access control endpoints;
- version negotiation, typed errors, request IDs, and read-only RBAC are tested;
- `ya` can list service, provider, account, model, and operation state in text and JSON;
- TUI dashboard reconnects safely and exposes no mutation actions.

This milestone is deployable to trusted pilot environments because it does not change provider credentials or routing.

### Milestone M3: Managed authentication

Issues: YA-TUI-06 and YA-TUI-07.  
Target: authenticate Copilot, Codex, and Kilo through daemon-owned operations.

Exit criteria:

- device-code operations survive TUI disconnect and enforce expiry/cancel semantics;
- API keys are write-only and never appear in responses, logs, audit, or diagnostic snapshots;
- environment-owned credentials are reported as read-only;
- Copilot/Codex multi-account behavior remains compatible;
- Kilo anonymous and API-key modes retain free-only and credential-precedence invariants;
- existing direct CLI commands either delegate to the local control API or fail safely while the daemon lock is held.

### Milestone M4: Safe mutation and operational TUI

Issues: YA-TUI-08 and YA-TUI-11.  
Target: enable/disable providers/accounts and manage supported routing/model settings without file editing.

Exit criteria:

- configuration changes support validation, dry run, revision conflict, audit, and rollback;
- provider replacements use atomic snapshot publication and bounded drain;
- failed changes retain the prior effective configuration;
- TUI confirms destructive/disruptive actions and exposes operation progress;
- listener/storage/process changes report restart-required rather than attempting self-restart;
- concurrent management and data-plane stress tests remain green.

### Milestone M5: Production distribution

Issues: YA-TUI-12 and YA-TUI-13.  
Target: supported systemd and Docker releases with security and compatibility evidence.

Exit criteria:

- packages install, upgrade, rollback, and uninstall without credential loss;
- systemd unit passes agreed sandbox analysis;
- Docker runs non-root with read-only root filesystem and dropped capabilities;
- Linux/macOS/Windows client artifacts and Linux server artifacts include checksums and SBOM;
- image/artifact signatures and provenance are published;
- N-1 client compatibility, failure recovery, load, PTY, and manual real-provider checks pass;
- operator runbooks cover install, connect, authenticate, rotate credential, backup, rollback, and incident redaction.

## 6. Ordered implementation backlog

The titles below are stable planning IDs linked to their implementation issues.

### [YA-TUI-01](https://github.com/duvu/ya-router/issues/7) — Refactor Go package and binary boundaries

Priority: P0  
Depends on: architecture acceptance  
Blocks: every implementation issue

Scope:

- move reusable runtime types out of the flat `package main` composition path;
- add internal package boundaries for config, providers, routing/proxy, runtime, and API contracts;
- introduce `cmd/ya-routerd` and `cmd/ya` build targets while retaining the existing compatibility binary;
- remove constructor side effects such as global health-registry replacement;
- keep provider implementations and data API behavior unchanged.

Acceptance:

- old and new service entrypoints produce the same tested data-plane behavior;
- `go test -race` covers package boundaries;
- no new third-party runtime dependency is introduced without explicit review;
- Docker and existing build stay functional.

### [YA-TUI-02](https://github.com/duvu/ya-router/issues/8) — Implement RuntimeManager and dynamic ProviderManager

Priority: P0  
Depends on: YA-TUI-01

Scope:

- immutable runtime snapshots;
- provider factories/descriptors;
- construct, validate, atomic swap, and drain lifecycle;
- independent health registry and provider event publication;
- removal/unregister and account/provider state reconciliation.

Acceptance:

- concurrent requests retain the snapshot they started with;
- failed replacement leaves the old provider active;
- provider drain is bounded and observable;
- race tests cover replace/remove/list/route operations.

### [YA-TUI-03](https://github.com/duvu/ya-router/issues/9) — Add single-writer state and revisioned configuration

Priority: P0  
Depends on: YA-TUI-01 and YA-TUI-02

Scope:

- daemon/process state lock;
- desired/effective/restart-required configuration model;
- monotonic revision and digest;
- conflict detection, validate/dry-run, last-known-good backup, and rollback primitives;
- file and parent-directory durability;
- secret references separated from ordinary configuration.

Acceptance:

- stale revision update fails deterministically;
- crash/fault injection cannot replace a valid config with a partial file;
- a second daemon fails with an actionable error;
- migration and rollback preserve current credentials and config path compatibility.

### [YA-TUI-04](https://github.com/duvu/ya-router/issues/10) — Establish the isolated Control API and security boundary

Priority: P0  
Depends on: YA-TUI-01 and YA-TUI-03

Scope:

- separate control listener;
- local Unix socket transport;
- opt-in remote HTTPS transport;
- control identities and viewer/operator/admin authorization;
- `GET /control/v1/meta`, feature/version negotiation, typed errors, request IDs, and idempotency framework;
- OpenAPI contract and security tests.

Acceptance:

- data API key is rejected by the control plane;
- non-loopback plaintext control binding is rejected;
- unauthorized and forbidden actions are distinct and audited without secret data;
- current and previous supported client versions negotiate predictably.

### [YA-TUI-05](https://github.com/duvu/ya-router/issues/11) — Expose read-only provider, account, model, config, and event resources

Priority: P1  
Depends on: YA-TUI-02, YA-TUI-03, and YA-TUI-04

Scope:

- provider descriptors and effective state;
- redacted accounts and credential source metadata;
- model catalog freshness, availability, and prefixed IDs;
- redacted desired/effective config views;
- operation/event read APIs and SSE resume.

Acceptance:

- all three providers appear whether enabled or disabled;
- no raw upstream account ID or secret appears;
- SSE resumes with `Last-Event-ID` and has polling fallback;
- catalog refresh errors do not discard last-known-good data.

### [YA-TUI-06](https://github.com/duvu/ya-router/issues/12) — Implement persistent asynchronous operations and auth sessions

Priority: P1  
Depends on: YA-TUI-03, YA-TUI-04, and YA-TUI-05

Scope:

- operation state machine and bounded persistence;
- create/get/list/cancel resources;
- owner, expiry, idempotency, reconnect, and event sequencing;
- restart recovery rules;
- auth-session resource for device code, API key, recovery token, and anonymous modes.

Acceptance:

- client disconnect does not cancel an operation;
- restart marks unsafe incomplete operations failed/expired without corrupting credentials;
- repeated idempotent creation returns one operation;
- operation failures are typed and redacted.

### [YA-TUI-07](https://github.com/duvu/ya-router/issues/13) — Add provider-managed authentication adapters and SecretStore

Priority: P1  
Depends on: YA-TUI-06

Scope:

- `AuthController` and `SecretStore` contracts;
- Copilot device-code and multi-account flows;
- Codex device-code, API-key, recovery-only token, and multi-account flows;
- Kilo anonymous/API-key flow;
- credential-source precedence and read-only environment sources;
- audit/redaction and key rotation behavior.

Acceptance:

- TUI/control clients never receive stored secrets;
- official Codex store remains read-only;
- environment credentials cannot be silently shadowed by a lower-precedence TUI write;
- real-provider checks are manual, redacted, and excluded from normal CI artifacts.

### [YA-TUI-08](https://github.com/duvu/ya-router/issues/14) — Add revision-safe management mutations and hot reload

Priority: P1  
Depends on: YA-TUI-02, YA-TUI-03, YA-TUI-06, and YA-TUI-07

Scope:

- provider/account enable, disable, priority, and removal;
- allowed models, default route, and model-map mutations;
- validation preview/diff, apply, rollback, and restart-required response;
- provider replacement and drain;
- management mutation audit.

Acceptance:

- two writers cannot silently overwrite each other;
- paid/anonymous and provider-prefix routing invariants remain enforced;
- a rejected/failed apply makes no effective data-plane change;
- no mutation triggers `systemctl`, Docker, shell, or process self-restart.

### [YA-TUI-09](https://github.com/duvu/ya-router/issues/15) — Build the ya client SDK and scriptable control commands

Priority: P1  
Depends on: YA-TUI-04 and YA-TUI-05

Scope:

- typed control client shared by TUI and commands;
- Unix socket and HTTPS/mTLS/OIDC transports;
- connection profiles and safe client credential references;
- timeouts, retry rules, version negotiation, stable exit codes, and JSON output;
- non-interactive provider/model/config/operation read commands.

Acceptance:

- commands work without a TTY;
- JSON output is contract-tested;
- mutation retries never violate idempotency;
- unsupported server/client combinations fail before mutation.

### [YA-TUI-10](https://github.com/duvu/ya-router/issues/16) — Deliver the read-only Bubble Tea TUI

Priority: P1  
Depends on: YA-TUI-05 and YA-TUI-09

Scope:

- connection, overview, providers/accounts, models, operation, and event screens;
- keyboard navigation, resize, monochrome/light/dark, reconnect, and offline states;
- sanitized errors and request IDs;
- Bubble Tea/Bubbles/Lip Gloss dependency pinning.

Acceptance:

- complete keyboard operation and no color-only status;
- PTY/golden tests cover supported terminal classes;
- TUI exit leaves daemon and operations running;
- no logs are written into the controlled terminal output.

### [YA-TUI-11](https://github.com/duvu/ya-router/issues/17) — Add authentication and safe mutation workflows to the TUI

Priority: P1  
Depends on: YA-TUI-07, YA-TUI-08, YA-TUI-09, and YA-TUI-10

Scope:

- device-code progress/reconnect/cancel;
- masked API-key and recovery-token input;
- anonymous enablement;
- provider/account actions;
- model refresh, allowlist, routing validate/diff/apply/rollback;
- confirmation and restart-required UX.

Acceptance:

- secret input is never rendered, persisted, or logged by the client;
- destructive actions require explicit confirmation;
- revision conflicts reload state instead of overwriting;
- disconnect/reconnect preserves operation visibility.

### [YA-TUI-12](https://github.com/duvu/ya-router/issues/18) — Package and harden systemd, Docker, client, and release artifacts

Priority: P2  
Depends on: YA-TUI-08, YA-TUI-09, and YA-TUI-11

Scope:

- systemd unit, dedicated user/group, runtime/state directories, sandboxing, package lifecycle;
- non-root/read-only/capability-dropped Docker and Compose examples;
- Linux server and Linux/macOS/Windows client artifacts;
- multi-arch images;
- checksums, SBOM, signing/provenance, version metadata, completions, and install docs.

Acceptance:

- package/image installation starts with least privilege and correct state permissions;
- upgrade/rollback preserves state and credentials;
- service correctly receives and drains on stop signals;
- release artifacts are reproducible enough to verify checksums and source commit.

### [YA-TUI-13](https://github.com/duvu/ya-router/issues/19) — Complete production E2E, security, recovery, and compatibility gates

Priority: P2  
Depends on: YA-TUI-01 through YA-TUI-12

Scope:

- mock-provider E2E across service/control/client/TUI;
- concurrent data traffic and management operations;
- crash/fault recovery;
- RBAC, transport, redaction, fuzz, and secret scanning;
- N-1 compatibility;
- systemd/Docker install/upgrade/rollback;
- manual redacted real-provider validation and operator runbooks.

Acceptance:

- all architecture acceptance gates have linked evidence;
- no open P0/P1 correctness or secret-handling defect remains;
- production release/rollback decision, owner, and observation window are documented;
- Go data-plane CI remains blocking.

## 7. Parallelization guidance

Safe parallel work:

- YA-TUI-03 and the descriptor portion of YA-TUI-04 after shared runtime types stabilize;
- YA-TUI-09 client transport and YA-TUI-05 read endpoints after the OpenAPI schema is fixed;
- YA-TUI-10 screen reducers while read-only fixtures are stable;
- systemd and Docker packaging subtests within YA-TUI-12.

Unsafe parallel work:

- TUI mutation UX before operation/config mutation contracts are accepted;
- provider auth adapters before secret and operation ownership is defined;
- direct config writers while the daemon single-writer lock is incomplete;
- Docker/systemd lifecycle controls embedded in the TUI;
- Rust control-plane implementation before Go behavior and fixtures stabilize.

## 8. Release strategy

### Preview 1: read-only local management

- `ya-routerd` preview service;
- local Unix socket;
- `ya` read commands and read-only TUI;
- no credential or config mutation.

### Preview 2: managed authentication

- daemon-owned device/API-key/anonymous flows;
- provider/account operations;
- secret-store migration preview;
- trusted local pilots only.

### Release candidate: safe mutation

- revisioned configuration and rollback;
- provider hot swap/drain;
- operational TUI;
- remote mTLS/OIDC management opt-in.

### Production release

- systemd and hardened Docker support;
- signed/SBOM release artifacts;
- compatibility and recovery evidence;
- documented rollout, observation, and rollback.

## 9. Rollout and rollback

1. Deploy the service with the control plane disabled; verify data-plane parity.
2. Enable local read-only control and observe provider/model state.
3. Enable managed authentication for one non-critical provider/account.
4. Migrate credential ownership with backup and rollback checkpoint.
5. Enable configuration mutations for admins.
6. Enable remote management only after transport and role mapping review.

At every step, rollback restores the prior binary and state schema backup. A new binary must not irreversibly rewrite state before its compatibility window is accepted.

## 10. Definition of done for every child issue

- Scope and non-goals are explicit.
- Relevant architecture decisions are linked.
- Unit/integration/race/security tests are included with the implementation.
- Secrets and raw account identifiers are absent from fixtures and logs.
- Documentation and OpenAPI/spec changes land with code.
- Existing Go data-plane checks pass.
- Upgrade/rollback impact is stated.
- Follow-up debt is filed rather than hidden in the PR body.

## 11. Deferred backlog after production v1

- Pluggable external secret-manager implementations beyond the first supported backend.
- Clustered control plane and high-availability state coordination.
- Web management UI using the same control API.
- Fine-grained custom RBAC policies.
- Provider plugin SDK and signed runtime plugins.
- Fleet view across several ya-routerd instances.
- Usage/cost analytics beyond operational metrics.

These items must not expand the first production release.
