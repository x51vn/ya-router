## Why

After completing the `refactor-project-structure` change, all production Go source code has been migrated to `internal/` and `cmd/`. The `src/` directory is now a legacy flat `package main` that duplicates everything in the new modular layout. Keeping it creates confusion about which code is authoritative, inflates the repo, and risks future changes landing in the wrong place.

## What Changes

- Remove all non-test Go source files from `src/` (the production code that has been ported to `internal/` and `cmd/`).
- Keep `src/*_test.go` files intact — they remain the integration and unit test suite and still reference the old `package main` symbols.
- Remove any other files in `src/` that are pure duplicates of new modules (e.g., helper stubs that only existed to support the old build).
- No changes to `internal/`, `cmd/`, `Makefile`, `Dockerfile`, or CI; the new layout is already authoritative.

## Capabilities

### New Capabilities

- `legacy-src-removal`: Remove redundant non-test production files from `src/` while preserving the test suite and ensuring `make test` still passes.

### Modified Capabilities

<!-- No spec-level requirement changes -->

## Impact

- `src/` shrinks to test files only; `package main` production symbols referenced by tests remain available via the still-present non-test stubs or via re-exporting.
- Build target `./cmd/github-copilot-svcs` is unaffected.
- `make test` must continue to pass for `./src/...` (tests call private helpers; those helpers must remain accessible — either kept as minimal stubs in `src/` or the tests are updated to call exported equivalents from `internal/`).
- CI `go test ./src/... || true` is unaffected because tests remain.
