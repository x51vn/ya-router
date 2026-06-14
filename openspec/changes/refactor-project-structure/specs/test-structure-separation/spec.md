## ADDED Requirements

### Requirement: Tests SHALL be organized separately from the flat production source layout
The repository SHALL stop treating tests as a flat mix alongside all production files and SHALL reorganize tests to align with the new module boundaries or a clearly separated integration test area.

#### Scenario: Developer identifies unit test scope
- **WHEN** a developer inspects tests for a specific runtime module
- **THEN** the related unit tests MUST be grouped with or clearly mapped to that module rather than hidden in an undifferentiated flat directory

### Requirement: Integration-oriented tests SHALL be distinguishable from unit tests
The repository SHALL provide a clear structural distinction between focused module tests and broader integration or end-to-end verification tests.

#### Scenario: CI or developer runs broader verification
- **WHEN** integration-oriented verification is reviewed or executed
- **THEN** those tests MUST be identifiable as broader-scope validation rather than indistinguishable from module-level unit tests

### Requirement: Test reorganization SHALL preserve validation intent
The test structure refactor SHALL preserve existing verification intent so that current coverage categories remain representable after files are moved.

#### Scenario: Coverage parity after migration
- **WHEN** the repository migration is completed
- **THEN** previously existing categories such as provider, proxy, auth, adapter, and integration validation MUST still exist in an equivalent organized form
