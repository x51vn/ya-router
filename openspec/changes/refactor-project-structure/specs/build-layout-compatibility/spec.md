## ADDED Requirements

### Requirement: Build commands SHALL remain operable after the layout refactor
The repository SHALL update its build and verification commands so contributors can still run the documented workflow successfully after source and test files move.

#### Scenario: Standard verification command is executed
- **WHEN** a contributor runs the documented verification sequence for the refactored repository
- **THEN** the command sequence MUST complete against the new layout without requiring undocumented manual path adjustments

### Requirement: Container and release workflows SHALL target the new layout
Docker and release automation SHALL be updated so container builds and release scripts reference the reorganized source tree correctly.

#### Scenario: Container image build after refactor
- **WHEN** automation or a developer builds the Docker image after the structural migration
- **THEN** the build MUST resolve the new source layout and produce the same deployable service binary

### Requirement: Developer documentation SHALL describe the new structure
Repository documentation SHALL explain the new source/test layout and the commands contributors must use to build, test, and navigate the refactored project.

#### Scenario: New contributor follows repository documentation
- **WHEN** a developer reads the project documentation after the refactor
- **THEN** they MUST be able to understand where source modules and tests live and how to run the supported workflows
