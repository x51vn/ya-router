# Change: Persist application logs with bounded file rotation

## Why

ya-router currently emits application diagnostics only to the process console.
Operators lose local history on restart and cannot inspect a bounded file log in
Docker or systemd deployments.

## What Changes

- Configure the shared standard logger to write to stderr and a local rotating
  log file.
- Add `logging.file_path`, `logging.max_file_size_mib`, and
  `logging.retained_files` to the persisted configuration schema.
- Default to `logs/ya-router.log`, 5 MiB per file, and exactly two retained
  files including the active file.
- Preserve console logging if the file cannot be initialized.
- Ensure the Docker image and systemd service can create the default relative
  log directory.
- Add automated coverage and operator documentation.

## Non-goals

- Introducing a second logging API, log shipping backend, or log-level policy.
- Rotating credentials, changing request payload logging, or retaining logs
  beyond the configured local bound.

## Impact

- Affected code: shared logger setup, persisted configuration, Docker entrypoint,
  systemd service, documentation, and tests.
- Affected operators: deployments gain local diagnostic files without losing
  console collection through Docker or systemd.
