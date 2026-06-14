## Context

The `refactor-project-structure` change successfully migrated all production Go code from `src/` (flat `package main`) to the new modular layout in `internal/` and `cmd/`. The 19 non-test `.go` files in `src/` are now duplicates of the authoritative code in `internal/config`, `internal/auth`, `internal/httputil`, `internal/provider`, `internal/proxy`, `internal/types`, and `cmd/github-copilot-svcs`.

However, `src/` still contains 12 `*_test.go` files that constitute the entire integration and unit test suite. These tests call private package symbols (unexported functions, types, and helpers) because they live in `package main` alongside the old source. This coupling is the key constraint.

## Goals / Non-Goals

**Goals:**
- Delete the 19 non-test production files from `src/` — they are dead weight after the refactor.
- `make test` continues to pass (`./src/...` test suite is green).
- `make build` continues to produce a working binary from `./cmd/github-copilot-svcs`.
- No changes to `internal/`, `cmd/`, or tooling.

**Non-Goals:**
- Moving or rewriting the `src/` test files (future work).
- Adding new tests to `internal/` packages (future work).
- Changing any runtime behavior.

## Decisions

**Decision: Keep minimal stubs in `src/` so tests still compile**

The 12 test files in `src/` reference unexported symbols: `extractModelFromBody`, `patchBodyModel`, `isChatGPTMode`, `resolveCodexAPIKey`, `resolveCodexChatGPTAuth`, `defaultConfig`, `modelsHandler`, `healthHandler`, `normalizeCopilotAccounts`, `normalizeCodexAccounts`, `buildTestConfigWithAccounts`, etc. They also use types (`Config`, `ModelList`, `Model`, `CopilotProvider`, `CodexProvider`, `ProviderRegistry`, `ProviderID`, `ProviderCopilot`, `ProviderCodex`, `Capability`, `CapabilityChat`, `CapabilityEmbeddings`, `ProviderHealth`, `ModelMapEntry`, `CopilotProviderConfig`, `CopilotAccount`, `CopilotAuthState`, `CodexAccount`, `CodexAuthState`, `ModelRouter`) and constants (`codexCredentialSourceEnv`, `codexCredentialSourceOfficialStore`, `codexCredentialSourceProxyConfig`).

Two alternatives:
1. **Stub approach** (chosen): Keep very thin `src/*.go` stub files that re-export or alias everything the tests need from `internal/`. Tests continue to compile with zero changes.
2. **Test rewrite approach**: Update all 12 test files to call exported `internal/` package APIs. Cleaner long-term but high effort, high risk of regressions, out of scope for a cleanup change.

The stub approach is preferred because it is minimal, safe, and reversible. Future work can migrate tests to `internal/` packages incrementally.

**Decision: Stubs use type aliases and var/func wrappers, not copy-paste**

Each stub file in `src/` will import the corresponding `internal/` package and re-export the types and functions tests need via type aliases (`type Foo = pkg.Foo`) and thin wrapper functions (`func foo(...) = pkg.foo`). This is a compile-time-safe thin layer, not another copy of logic.

## Risks / Trade-offs

- **Stub maintenance burden**: Stubs must stay in sync with `internal/` signatures. If internal APIs change, stubs break at compile time (not silently). This is acceptable because the failure is loud and local.
- **`package main` stubs importing `internal/`**: Valid — `cmd/github-copilot-svcs` already does this; `src/` can too since it's in the same module.
- **Circular imports**: No risk — stubs import `internal/` packages, which do not import `src/`.

## Migration Plan

1. Audit each non-test `src/` file and list every symbol the test files reference.
2. Create minimal stub files that satisfy those references via aliases/wrappers.
3. Delete the 19 original non-test production files.
4. Run `make fmt && make vet && make test && make build` to verify.
5. Commit.

No rollback complexity — if tests break, restore deleted files from git.
