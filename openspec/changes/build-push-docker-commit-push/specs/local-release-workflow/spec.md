## ADDED Requirements

### Requirement: Single-command local release
The system SHALL provide a `make release` target that executes the full local release cycle in sequence: Go build → Docker image build → Docker image push → git commit → git push.

#### Scenario: Successful full release
- **WHEN** developer runs `make release` with all required env vars set (`DOCKER_REGISTRY_HOST`, `DOCKER_REGISTRY_USER`, `DOCKER_REGISTRY_PASSWORD`)
- **THEN** the Go binary is built, Docker image is built and pushed to the registry with versioned and `latest` tags, local changes are committed, and the codebase is pushed to the remote git repository

#### Scenario: Missing env var aborts before any destructive step
- **WHEN** developer runs `make release` with one or more required env vars missing or empty
- **THEN** the script SHALL print a clear error message naming the missing variable and exit with non-zero status before performing any build, push, or git operation

### Requirement: Composable Docker sub-targets
The system SHALL provide independent `make docker-build` and `make docker-push` targets usable outside the full release flow.

#### Scenario: Build Docker image only
- **WHEN** developer runs `make docker-build`
- **THEN** the Docker image is built and tagged locally without pushing to the registry

#### Scenario: Push Docker image only
- **WHEN** developer runs `make docker-push` with required env vars set
- **THEN** the previously built Docker image is pushed to the registry with versioned and `latest` tags without rebuilding

### Requirement: Configurable version tag
The system SHALL default to a `YYYYMMDD-local` version string and SHALL allow override via a `VERSION` environment variable.

#### Scenario: Default version tag used
- **WHEN** developer runs `make release` without setting `VERSION`
- **THEN** the Docker image is tagged with `YYYYMMDD-local` (current date) and the git commit message includes the same version string

#### Scenario: Custom version tag override
- **WHEN** developer runs `VERSION=v2.1.0 make release`
- **THEN** all Docker tags and git commit messages use `v2.1.0` as the version string

### Requirement: Dry-run mode
The system SHALL support `DRY_RUN=1` to print all commands that would execute without performing any push, commit, or registry operation.

#### Scenario: Dry-run skips destructive steps
- **WHEN** developer runs `DRY_RUN=1 make release`
- **THEN** the script prints each command it would run and exits successfully without pushing to Docker registry, committing, or pushing to git
