# Change: Add a managed service control plane and TUI client

## Why

ya-router currently combines server startup, provider authentication, configuration mutation, model inspection, and proxy serving in one CLI-oriented binary. Provider instances are registered at startup and auth commands write the same JSON configuration outside the running process. Adding a TUI directly to those paths would create multiple writers, weak separation between model-client and administrative authority, and no safe hot-reload contract.

Operators need a production-capable way to deploy ya-router as a daemon and manage it from an OS client without exposing provider secrets or granting Docker/systemd privileges.

## What changes

- Split distribution responsibilities into `ya-routerd` and the `ya` client while preserving compatibility wrappers.
- Introduce a daemon-owned runtime manager, provider lifecycle, revisioned state, and secret-store boundary.
- Add a separate versioned control API with local Unix-socket and opt-in remote TLS transports.
- Add asynchronous auth/refresh operations that survive client disconnects.
- Add read-only and mutation resources for providers, accounts, models, and supported routing/configuration.
- Add a Bubble Tea-based TUI and scriptable client commands using the same typed client library.
- Add systemd and hardened Docker packaging, audit, observability, compatibility, and release gates.

## Non-goals

- Web UI, clustered control plane, runtime provider plugins, or fleet management.
- TUI access to Docker, systemd, a shell, arbitrary files, or arbitrary upstream URLs.
- Secret export or storage of secrets in ordinary config resources.
- Changes to deterministic routing, provider credential isolation, or `/v1/*` semantics.
- Combining this change with a Rust production cutover.

## Delivery

This proposal defines the target and ordered roadmap. Implementation is split into the GitHub issues linked from `docs/roadmaps/managed-service-and-tui-roadmap.md`; no runtime implementation is included in the planning change.

