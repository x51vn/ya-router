## 1. Target Structure Definition

- [x] 1.1 Define the destination package/module tree for CLI/bootstrap, config, auth, providers, proxy/API handling, routing, and shared domain helpers.
- [x] 1.2 Map every current file in `src/` to its target module or test location and document any files that must remain as thin entrypoints.

## 2. Source Code Refactor

- [x] 2.1 Introduce the new executable entrypoint structure while preserving the single `github-copilot-svcs` binary output.
- [x] 2.2 Extract configuration and bootstrap concerns into their target module boundaries and keep the repository compiling.
- [x] 2.3 Extract auth and provider-related code into dedicated modules and update imports/call sites.
- [x] 2.4 Extract proxy, routing, adapter, and shared request/response logic into dedicated modules and update imports/call sites.

## 3. Test Structure Refactor

- [x] 3.1 Reorganize unit tests so they align with the new module boundaries instead of remaining in one flat mixed directory.
- [x] 3.2 Move broader integration-style tests into a clearly distinguishable integration test area and preserve equivalent validation intent.
- [x] 3.3 Verify that provider, proxy, auth, adapter, and integration coverage categories remain represented after the moves.

## 4. Tooling and Documentation Updates

- [x] 4.1 Update `Makefile`, `Dockerfile`, and release/build scripts to target the new source and test layout.
- [x] 4.2 Update `README.md`, `AGENTS.md`, and related docs to explain the new structure and supported build/test workflows.
- [x] 4.3 Run `make fmt && make vet && make test && make build` against the refactored layout and fix any migration regressions.
