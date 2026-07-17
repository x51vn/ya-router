## ADDED Requirements

### Requirement: Shared application logs have console and bounded file sinks
The system SHALL write every shared standard-library application log entry to
stderr and a configured local file. The default file SHALL be
`logs/ya-router.log`, its parent directory SHALL be created during startup, and
the active log SHALL rotate at 5 MiB.

#### Scenario: Default startup creates the active log file
- **WHEN** ya-router starts with logging settings omitted
- **THEN** it SHALL create `logs/ya-router.log` and continue writing to stderr

#### Scenario: Shared application log entry reaches both sinks
- **WHEN** an application component writes through the shared logger
- **THEN** the entry SHALL be observable on stderr and in the active log file

### Requirement: Retained file storage remains bounded
The system SHALL retain at most two log files total, including the active file.
After repeated size-based rotations, it SHALL remove older rotated files so the
default retained storage is approximately 10 MiB or less, excluding filesystem
metadata and rotation overhead.

#### Scenario: Repeated rotation removes the oldest backup
- **WHEN** writes repeatedly exceed the configured 5 MiB threshold
- **THEN** only the active file and most recent rotated backup SHALL remain

### Requirement: File logging failures preserve service availability
The system SHALL report file-logging initialization failures clearly to stderr
and SHALL continue with console logging.

#### Scenario: Configured log file cannot be initialized
- **WHEN** the configured file path cannot be created or opened
- **THEN** ya-router SHALL emit a file-logging-disabled error to stderr and
  continue processing application logs through the console sink

### Requirement: Deployment defaults provide a writable log location
The Docker and systemd deployment paths SHALL allow the service user to create
the default relative log directory without weakening service security settings.

#### Scenario: Docker service handles a request
- **WHEN** the Docker image starts as its unprivileged service user and receives
  a request that emits an application log entry
- **THEN** the entry SHALL be present in `/app/logs/ya-router.log`
