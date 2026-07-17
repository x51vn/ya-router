# Tasks

## Planning baseline

- [x] Document the service/client boundary and naming decision.
- [x] Define control/data listener separation and management trust model.
- [x] Define runtime snapshot, provider lifecycle, state revision, and secret-store contracts.
- [x] Define operation/auth-session and provider/account/model resource contracts.
- [x] Define TUI, automation, systemd, Docker, release, and compatibility requirements.
- [x] Publish an ordered implementation roadmap.
- [x] Link [GitHub epic #20](https://github.com/duvu/ya-router/issues/20) and child issues #7 through #19 into the roadmap and PR.

## Implementation tracking

- [x] [YA-TUI-01 (#7)](https://github.com/duvu/ya-router/issues/7) refactor package and binary boundaries.
- [x] [YA-TUI-02 (#8)](https://github.com/duvu/ya-router/issues/8) add runtime/provider manager.
- [x] [YA-TUI-03 (#9)](https://github.com/duvu/ya-router/issues/9) add revisioned single-writer state.
- [x] [YA-TUI-04 (#10)](https://github.com/duvu/ya-router/issues/10) add isolated control API/security foundation.
- [x] [YA-TUI-05 (#11)](https://github.com/duvu/ya-router/issues/11) add read-only control resources.
- [x] [YA-TUI-06 (#12)](https://github.com/duvu/ya-router/issues/12) add async operation/auth-session framework.
- [x] [YA-TUI-07 (#13)](https://github.com/duvu/ya-router/issues/13) add provider auth adapters and secret store.
- [x] [YA-TUI-08 (#14)](https://github.com/duvu/ya-router/issues/14) add revision-safe mutations and hot reload.
- [x] [YA-TUI-09 (#15)](https://github.com/duvu/ya-router/issues/15) add client SDK and scriptable commands.
- [x] [YA-TUI-10 (#16)](https://github.com/duvu/ya-router/issues/16) add read-only TUI.
- [x] [YA-TUI-11 (#17)](https://github.com/duvu/ya-router/issues/17) add auth and mutation TUI workflows.
- [x] [YA-TUI-12 (#18)](https://github.com/duvu/ya-router/issues/18) add production packaging/hardening.
- [ ] [YA-TUI-13 (#19)](https://github.com/duvu/ya-router/issues/19) complete production acceptance gates.
