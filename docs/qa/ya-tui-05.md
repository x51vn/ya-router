# YA-TUI-05 acceptance evidence

Issue: [#11](https://github.com/duvu/ya-router/issues/11)

The read-only Control API implementation is validated against the following invariants:

- provider reads include every compiled-in provider, whether enabled or disabled;
- account and configuration responses expose daemon-owned identifiers and credential-source metadata but never raw secrets or upstream account identifiers;
- model refresh failures retain the last successful catalog and mark it stale with a sanitized error code;
- lifecycle events support both polling with `after` and resumable SSE with `Last-Event-ID`;
- the audit response wrapper preserves `http.Flusher`, so SSE remains functional through the shared audit middleware.

Validation gate:

```text
gofmt verification
go vet ./...
go test -race -count=1 ./...
build ya-router, ya-routerd, and ya
```
