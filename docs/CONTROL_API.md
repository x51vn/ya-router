# Control API foundation

`ya-routerd` serves management traffic separately from the OpenAI-compatible
data plane. The Control API exposes discovery, redacted provider/account/model/
config/routing state, durable operations, write-only secret posture, auth
sessions, and revision-safe supported mutations:

```text
GET /control/v1/meta
```

All resources remain version-negotiated and use the same private local socket
by default.

## Local transport

A private Unix socket is enabled by default next to the configured state file:

```text
<config-directory>/control.sock
```

The socket is created with mode `0600`. A systemd package may override the path
with `YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock`. Set the variable to
`off` only when a valid remote listener is configured; disabling every control
listener fails startup.

Two optional variables widen access without loosening the owner-only default:

| Environment variable | Default | Purpose |
|---|---|---|
| `YA_ROUTER_CONTROL_SOCKET_MODE` | `0600` | Octal file mode applied to the socket |
| `YA_ROUTER_CONTROL_SOCKET_GROUP` | empty | Group (name or numeric GID) granted access via `chown`; owning user is unchanged |

`YA_ROUTER_CONTROL_SOCKET_GROUP` lets a trusted group (for example, an
operator group managed by the systemd unit) reach the socket without running
as the `ya-router` service account. Startup fails if the named group cannot be
resolved.

Local socket access maps to the `admin` role because filesystem ownership and
socket permissions form the trust boundary. The Control API still rejects an
explicit `YA_ROUTER_API_KEY` presented as a control credential.

Example with curl:

```bash
curl --unix-socket ~/.local/share/github-copilot-svcs/control.sock \
  -H 'X-YA-Client-Version: 1.1.0' \
  http://unix/control/v1/meta
```

## Optional loopback TLS

A loopback TCP listener is opt-in and always uses HTTPS:

```bash
export YA_ROUTER_CONTROL_LISTEN_ADDRESS=127.0.0.1:7443
export YA_ROUTER_CONTROL_TLS_CERT=/etc/ya-router/control-server.pem
export YA_ROUTER_CONTROL_TLS_KEY=/etc/ya-router/control-server-key.pem
export YA_ROUTER_CONTROL_ADMIN_TOKEN="$(openssl rand -hex 32)"
```

Independent token variables are available for `viewer`, `operator`, and
`admin`:

```text
YA_ROUTER_CONTROL_VIEWER_TOKEN
YA_ROUTER_CONTROL_OPERATOR_TOKEN
YA_ROUTER_CONTROL_ADMIN_TOKEN
```

These values must differ from `YA_ROUTER_API_KEY`. Control tokens are a
loopback TLS bootstrap mechanism; they are not accepted as a replacement for
mTLS on a non-loopback listener.

## Non-loopback remote administration

A non-loopback control listener fails closed unless all of the following are
configured:

- server TLS certificate and private key;
- client CA trust file;
- mandatory verified client certificates;
- at least one certificate subject-to-role mapping.

```bash
export YA_ROUTER_CONTROL_LISTEN_ADDRESS=0.0.0.0:7443
export YA_ROUTER_CONTROL_TLS_CERT=/etc/ya-router/control-server.pem
export YA_ROUTER_CONTROL_TLS_KEY=/etc/ya-router/control-server-key.pem
export YA_ROUTER_CONTROL_CLIENT_CA=/etc/ya-router/control-client-ca.pem
export YA_ROUTER_CONTROL_ADMIN_SUBJECTS='spiffe://example/ya-admin'
export YA_ROUTER_CONTROL_OPERATOR_SUBJECTS='spiffe://example/ya-operator'
export YA_ROUTER_CONTROL_VIEWER_SUBJECTS='spiffe://example/ya-viewer'
```

Mappings are comma-separated and can match a URI SAN, the full X.509 subject,
or the Common Name. URI SAN values are preferred. Non-loopback plaintext
startup is rejected.

## Authorization

Roles are ordered:

| Role | Foundation permission |
|---|---|
| `viewer` | Read `/control/v1/meta` and future redacted resources |
| `operator` | Viewer permissions plus future operational commands |
| `admin` | Operator permissions plus future configuration and rollback commands |

Authentication failures return `401`. An authenticated identity with an
insufficient role returns `403`. Both outcomes produce redacted audit events.
Audit events include actor, role, method, path, result, status, request ID, and
timestamp; they exclude headers, bodies, query strings, credentials, device
codes, and raw account identifiers.

## Version negotiation

Clients send `X-YA-Client-Version`. The current foundation accepts the current
client line (`1.1.x`) and previous line (`1.0.x`). `/control/v1/meta` remains
available to unsupported clients and returns `compatible=false`; other control
resources reject unsupported versions with `426` before executing work.

All responses include `X-Request-ID`. Errors use a stable envelope:

```json
{
  "error": {
    "code": "control_forbidden",
    "message": "The authenticated identity does not have the required role.",
    "retryable": false,
    "request_id": "req_...",
    "details": {
      "required_role": "admin"
    }
  }
}
```

## Idempotency framework

Mutation and operation-creation routes are registered as idempotent and require an
`Idempotency-Key` header. The service serializes concurrent duplicates,
replays the first response for an identical payload, and returns `409` if the
same key is reused with a different payload. Request bodies are bounded and a
panic is converted into a typed, replayable `500` response rather than leaving
waiters blocked.

## Contract

The machine-readable contract is
[`docs/api/control-v1.openapi.yaml`](api/control-v1.openapi.yaml).

## Read-only resources

The viewer role can inspect the complete redacted daemon read model:

| Endpoint | Purpose |
|---|---|
| `GET /control/v1/providers` | All compiled-in provider descriptors, including disabled providers, effective capabilities, generation, and sanitized health |
| `GET /control/v1/accounts` | Daemon-owned account IDs, labels, priority, and credential-source metadata without tokens or upstream account IDs |
| `GET /control/v1/models` | Per-provider prefixed model catalogs with availability, freshness, and last-known-good refresh state |
| `GET /control/v1/config` | Redacted desired/effective configuration, revision, digests, and restart-required paths |
| `GET /control/v1/routing/thiendu` | Capability-specific configured candidates, selected target, stable skip reasons, cooldowns, and bounded counters |
| `GET /control/v1/operations` | Stable polling collection; populated by the async-operation implementation |
| `GET /control/v1/events?after=N` | Bounded lifecycle-event polling fallback |
| `GET /control/v1/events/stream` | Resumable Server-Sent Events stream |
| `GET /control/v1/secrets` | Redacted credential posture (slot, source, configured, read-only, version) — never secret values |

Use `refresh=true` on the model resource to force an upstream refresh. A failed
refresh records the generic `catalog_refresh_failed` state but does not discard
the last successful catalog. Model IDs are always provider-prefixed.

SSE clients resume by sending `Last-Event-ID`. The server subscribes before
replaying retained history and de-duplicates by monotonic sequence, preventing
an event gap between replay and live delivery. Clients that cannot maintain an
SSE connection can poll `/events?after=<last sequence>` and persist
`next_after`.

## Revision-safe configuration mutations

Operator-role clients apply online configuration changes through a single
compare-and-swap endpoint. Each mutation carries an `expected_revision`; the
daemon rejects a stale write with a typed `revision_conflict` (HTTP 409) so two
writers cannot silently overwrite each other. `dry_run: true` validates and
returns the diff (`changed_paths`, `restart_required`) without committing. A
committed change is hot-reloaded by reconciling the provider runtime — never by
invoking systemd, Docker, a shell, or a process self-restart. A rejected
validation or conflict makes no effective data-plane change.

| Endpoint | Purpose |
|---|---|
| `POST /control/v1/config/mutations` | Apply one revision-safe mutation (requires `Idempotency-Key`) |

Supported mutation kinds: `provider_enabled`, `default_model`,
`default_provider`, `allowed_models`, `model_map_set`, `model_map_delete`. A
`model_map_set` key may not collide with a configured umbrella (virtual) model,
preserving routing precedence.

## The `ya` control client

The installable `ya` binary opens the keyboard-driven daemon dashboard when
called without a subcommand. It speaks the local Unix socket by default (or
HTTPS/mTLS via `--address`) and never owns daemon lifecycle. The dashboard
shows daemon readiness, providers, `thiendu` routing/cooldowns, catalogs,
operations, and lifecycle events; its palette can authenticate providers,
write masked API keys, refresh catalogs, cancel auth operations, and apply
revision-safe provider enable/disable changes.

Scriptable commands work without a TTY and support `--json`: `meta`,
`providers`, `accounts`, `models`, `config`, `routing`, `operations`,
`operation`, `events`, and `secrets`. Mutation commands require `--revision`
and support `--dry-run`: `provider-enable`, `provider-disable`,
`default-model`, `default-provider`, `allowed-models`, `model-map-set`, and
`model-map-delete`. `auth-start`, `auth-cancel`, `secret-set --stdin`, and
`secret-delete` cover the corresponding write-only control paths.

Mutations send a generated `Idempotency-Key` that is reused across retries, so a
retried mutation is deduplicated by the daemon rather than applied twice. Exit
codes are stable: `0` ok, `2` usage, `3` connection, `4` auth, `5` forbidden,
`6` not-found, `7` incompatible, `8` conflict, `9` server.

## Persistent operations and authentication sessions

Long-running control work is represented by durable operation records stored in
`operations.json` beside the revisioned configuration by default. Override the
path with `YA_ROUTER_OPERATIONS_PATH` when deployment packaging requires a
separate state volume.

| Endpoint | Purpose |
|---|---|
| `POST /control/v1/operations` | Create a bounded provider-neutral operation reservation |
| `GET /control/v1/operations/{id}` | Reconnect to an owned operation; administrators may inspect all operations |
| `DELETE /control/v1/operations/{id}` | Cancel an owned cancelable operation |
| `GET /control/v1/operations/events?after=N` | Poll persisted operation lifecycle events |
| `GET /control/v1/operations/events/stream` | Resume operation events using `Last-Event-ID` |
| `POST /control/v1/auth-sessions` | Create a provider-neutral auth session for a supported provider and method |
| `GET /control/v1/auth-sessions/{id}` | Reconnect to an auth session |
| `DELETE /control/v1/auth-sessions/{id}` | Cancel an auth session |

Creation requires `Idempotency-Key`. The key is hashed with the authenticated
owner and persisted with a canonical request digest. Repeating the same request,
even after daemon restart, returns the original operation; reusing the key with
a different request returns `409`.

Operation workers use daemon-owned contexts, so loss of the initiating HTTP or
SSE connection does not cancel work. On restart, non-terminal work is never
silently resumed: unsafe work becomes `failed` with `operation_interrupted`,
while expiry-oriented auth sessions become `expired`. Failures use typed generic
messages and never persist raw upstream error text.

The auth-session creation contract accepts only provider, daemon-owned account
ID, method, and expiry. API keys, recovery tokens, device codes, and other secret
fields are rejected here; provider adapters and write-only secret handling are
implemented by the next authentication milestone.
