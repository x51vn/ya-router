# Tasks

## 1. Shared logging and configuration

- [x] 1.1 Add bounded logging settings and defaults to the persisted config.
- [x] 1.2 Add one shared stderr-plus-rotating-file logger during daemon startup.
- [x] 1.3 Fall back to console logging with a visible initialization error.

## 2. Deployment and documentation

- [x] 2.1 Prepare the default log directory for Docker and systemd deployment.
- [x] 2.2 Document settings, defaults, rotation, retention, and fallback.

## 3. Validation evidence

- [x] 3.1 Add red/green tests for creation, dual writing, rotation, retention,
  concurrent writes, and fallback.
- [x] 3.2 Run `make check`, shell and JSON validation, and the Docker build.
- [x] 3.3 Run the built Docker service, call `/health` and an invalid chat
  request, and observe the request log in the configured file.
