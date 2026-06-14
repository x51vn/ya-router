## Why

The current project keeps all production code and most tests together in one flat `src/` directory, which makes boundaries between runtime concerns, transport layers, auth flows, and tests harder to understand and maintain. A structural refactor is needed now because the codebase has grown to cover multiple providers, response adapters, release workflows, and OpenSpec-driven changes, making the flat layout an increasing source of friction.

## What Changes

- Reorganize the codebase into clearer source and test boundaries instead of keeping nearly all Go files in one flat directory.
- Define a modular package layout that separates core runtime concerns such as CLI/bootstrap, provider integrations, HTTP/API proxying, auth, routing, config, and shared model/transform utilities.
- Move tests out of the mixed flat layout into a dedicated test structure aligned with the new module boundaries while preserving current verification coverage.
- Update build, test, Docker, and release workflows so they target the new package layout consistently.
- Update developer documentation and project conventions to describe the new directory structure and module responsibilities.
- **BREAKING**: internal repository layout and import paths will change, requiring build/test command updates and coordinated file moves.

## Capabilities

### New Capabilities
- `modular-project-layout`: Define and enforce a maintainable package/module structure for source code and tests.
- `test-structure-separation`: Separate test code organization from production code organization while preserving equivalent validation behavior.
- `build-layout-compatibility`: Ensure build, test, Docker, and release workflows operate correctly after the repository layout changes.

### Modified Capabilities
- None.

## Impact

- Affected code: all Go source files under `src/`, associated `*_test.go` files, and bootstrap entrypoints such as `main.go`.
- Affected tooling: `Makefile`, `Dockerfile`, release/deploy scripts, and any commands that assume `./src/...` as the only target.
- Affected documentation: `README.md`, `AGENTS.md`, and any operator/developer docs describing build and test flows.
- Affected systems: local development workflow, CI verification flow, and container build/release flow.
