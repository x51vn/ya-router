## Context

The service uses the Go standard package logger throughout providers, routing,
control, and HTTP handling. Replacing it would risk separate unmanaged outputs.
The existing configuration file is the durable operator configuration mechanism.

## Decisions

### Shared standard logger with a rotating writer

Use `io.MultiWriter` to retain the stderr sink and add a single
`lumberjack.Logger` file sink. The library serializes writes and rotates when a
write would cross its size threshold. Setting `MaxBackups` to one retains one
rotated file plus the active file, satisfying the two-file bound.

### Create the file before serving

Create the parent directory and active file during logger setup, before the
daemon builds providers or starts listeners. A creation failure is written to
the console sink and leaves the standard logger directed at stderr.

### Keep deployment-relative paths writable

The Docker entrypoint creates and owns `/app/logs` before it drops privileges.
The systemd unit uses `/var/lib/ya-router` as its working directory, which is
the runtime-owned state directory. In both deployments the default relative
path resolves to a writable location.

## Risks and mitigations

- File initialization can fail because of an invalid path or permissions.
  The service remains available with a clear stderr message and console logs.
- Rotation behavior is library-owned. Automated tests exercise the configured
  limit and concurrent writes, while the dependency provides the synchronized
  rotation implementation.

## Rollback

Revert the logger setup, configuration fields, and deployment path preparation,
then deploy the preceding image. Console logging remains the existing fallback.
