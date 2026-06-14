## ADDED Requirements

### Requirement: Non-test production files in `src/` SHALL be removed after migration to `internal/`
After the `refactor-project-structure` migration, `src/` SHALL contain only `*_test.go` files. The 19 non-test `.go` files that duplicate `internal/` and `cmd/` code SHALL be deleted.

#### Scenario: Developer inspects src/ after cleanup
- **WHEN** a developer lists files in `src/`
- **THEN** only `*_test.go` files SHALL be present; no `package main` production source SHALL remain

### Requirement: The `src/` test suite SHALL continue to compile and pass after file removal
Because existing tests in `src/` call unexported symbols from the old `package main`, thin stub files SHALL be introduced in `src/` that expose those symbols via type aliases and wrapper functions delegating to `internal/` packages.

#### Scenario: make test runs after cleanup
- **WHEN** `make test` is executed
- **THEN** `go test ./internal/... ./cmd/... ./src/...` MUST exit 0 with all tests passing or marked as no test files

#### Scenario: Tests reference private helpers after cleanup
- **WHEN** a test in `src/` calls an unexported function (e.g., `defaultConfig()`, `extractModelFromBody()`, `isChatGPTMode()`)
- **THEN** the function MUST resolve to a stub in `src/` that delegates to the corresponding exported `internal/` package function or type alias

### Requirement: The production build target SHALL remain `./cmd/github-copilot-svcs`
The stub approach in `src/` MUST NOT affect the binary produced by `make build`. The build target is `./cmd/github-copilot-svcs` and the stubs are test-support code only.

#### Scenario: make build after cleanup
- **WHEN** `make build` is executed
- **THEN** the `github-copilot-svcs` binary MUST be produced from `./cmd/github-copilot-svcs` with no errors

### Requirement: Stubs SHALL NOT duplicate business logic
Stub files in `src/` SHALL only contain type aliases (`type Foo = pkg.Foo`) and thin delegating wrapper functions. No business logic, no copy-paste of `internal/` code.

#### Scenario: Stub file contents are reviewed
- **WHEN** a developer reads a stub file in `src/`
- **THEN** each declaration SHALL either be a `type Alias = internal.Type` alias or a one-line function wrapper; no logic blocks SHALL be present
