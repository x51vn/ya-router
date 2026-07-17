# YA-TUI-06 acceptance evidence

Issue: [#12](https://github.com/duvu/ya-router/issues/12)

Implemented invariants:

- operation records and lifecycle events are bounded and durably persisted with
  restrictive permissions, file sync, atomic rename, and directory sync;
- workers use daemon-owned contexts rather than initiating request contexts;
- restart recovery deterministically fails unsafe incomplete work or expires
  expiry-oriented auth sessions;
- owner-scoped idempotency keys and request digests survive daemon restart;
- operation failures are typed and use generic redacted messages;
- list/get/cancel and operation event polling/SSE enforce owner or administrator
  visibility;
- auth-session creation rejects raw secret fields and supports provider-neutral
  `device_code`, `api_key`, `manual_token_recovery`, and `anonymous` contracts.

Validation gate:

```text
gofmt verification
go vet ./...
go test -race -count=1 ./...
build ya-router, ya-routerd, and ya
OpenAPI YAML parse
```
