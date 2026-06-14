## Context

The repository currently uses a flat `src/` directory where almost every runtime concern is implemented as `package main`, and tests are colocated in the same directory with production files. This worked when the service was smaller, but the project now includes multiple provider integrations, response adapters, auth flows, release utilities, and spec-driven change management. The refactor must preserve the single-binary service behavior while creating clearer source boundaries, separating test organization, and keeping local build, Docker, and release workflows operational.

Constraints include preserving the OpenAI-compatible API surface, avoiding unnecessary external dependencies, and keeping the repository easy to build with existing Make-based commands. The current deployment and Docker flow assume a buildable CLI entrypoint and stable binary output name, so the refactor must maintain those operator-facing behaviors even if the internal package layout changes.

## Goals / Non-Goals

**Goals:**
- Establish a modular source layout that groups related runtime code by responsibility instead of keeping all files in one flat directory.
- Separate production source organization from test organization so tests no longer appear as a flat mix alongside all runtime files.
- Preserve the single compiled binary, existing endpoint behavior, and existing release/deployment workflows after the refactor.
- Provide a migration path for build, test, Docker, and documentation updates so contributors can adopt the new layout safely.

**Non-Goals:**
- Rewriting provider logic, routing behavior, or auth flows beyond what is necessary for package extraction.
- Changing the external HTTP API contract, configuration schema, or deployment topology.
- Introducing third-party dependencies or a new build system as part of this refactor.
- Redesigning every internal abstraction at once; the focus is structural separation, not wholesale behavior changes.

## Decisions

### 1. Split runtime code into responsibility-based internal packages while keeping a thin CLI entrypoint
The refactor will move code from the flat `src/` directory into a small top-level command entrypoint and a set of focused packages for API/proxy handling, provider integrations, auth, config, routing, and shared domain/model helpers. This keeps the single-binary architecture while reducing cognitive load and file sprawl.

**Rationale:** The current flat `package main` layout makes ownership and dependency direction hard to understand. Responsibility-based packages improve readability and make future changes less risky.

**Alternatives considered:**
- Keep the flat layout and only rename files: rejected because it does not solve dependency sprawl.
- Split into many micro-packages immediately: rejected because it increases churn and import complexity more than needed.

### 2. Separate tests by module boundary, with package-aligned unit tests and a distinct integration test area
Unit tests will move alongside the relevant new packages or into package-specific test locations, while broader integration or end-to-end checks will be grouped into a clearer integration-focused area. The key requirement is that tests are organized by concern rather than remaining in a single flat directory.

**Rationale:** Test intent becomes clearer when tests mirror runtime boundaries and when integration tests are visibly distinct from unit tests.

**Alternatives considered:**
- Move all tests into one global `tests/` directory: rejected because it can weaken proximity between code and focused unit coverage.
- Leave tests mixed with runtime files: rejected because it preserves the current maintainability problem.

### 3. Preserve build ergonomics through stable top-level commands and compatibility updates
The refactor will update `Makefile`, `Dockerfile`, and release scripts so contributors can still use predictable top-level commands, even if the underlying build target changes from the current flat layout assumptions.

**Rationale:** Structural improvements should not degrade operator or contributor ergonomics.

**Alternatives considered:**
- Require contributors to learn multiple new low-level `go build` targets immediately: rejected because it raises migration cost.
- Keep outdated build targets temporarily without updating docs: rejected because it creates drift and confusion.

### 4. Migrate incrementally by moving one concern at a time behind compiling checkpoints
The refactor should proceed in compile-safe stages such as extracting config/bootstrap, then auth/provider layers, then proxy/api layers, then reorganizing tests and docs.

**Rationale:** This reduces breakage risk in a repo that already has active deployment and release workflows.

**Alternatives considered:**
- Perform a single massive move in one step: rejected because it makes regressions harder to isolate and recover from.

## Risks / Trade-offs

- [Import path churn across many files] → Mitigation: move code in bounded stages and keep package responsibilities explicit.
- [Build or Docker workflow regression] → Mitigation: update `Makefile`, `Dockerfile`, and release scripts in the same migration sequence and verify with the standard command chain.
- [Tests becoming harder to run during transition] → Mitigation: preserve a single top-level verification entrypoint and migrate integration tests only after package boundaries stabilize.
- [Over-fragmentation into too many packages] → Mitigation: prefer a small number of responsibility-based modules rather than fine-grained micro-packages.
- [Developer confusion during migration] → Mitigation: document the new structure and expected build/test commands in `README.md` and `AGENTS.md`.

## Migration Plan

1. Define the target package structure and map current files to destination modules.
2. Introduce the new command entrypoint and initial internal packages without changing external behavior.
3. Migrate source files module by module, keeping the repository buildable after each stage.
4. Reorganize tests to align with the new module boundaries and isolate integration coverage clearly.
5. Update build/test/Docker/release tooling to target the new layout.
6. Refresh documentation and contributor guidance.
7. Run the standard verification sequence before final rollout.

Rollback strategy: if a migration stage introduces instability, revert the most recent structural move and restore the previous compiling checkpoint before attempting the next extraction.

## Open Questions

- Should integration tests live under a dedicated top-level `tests/` directory or remain near source packages under an `integration` grouping?
- Which package boundary should own cross-cutting shared request/response models to avoid circular imports?
- Whether some top-level files such as CLI/config/bootstrap should remain outside `internal/` for repository simplicity or be moved entirely into internal packages with a minimal `main` wrapper.
