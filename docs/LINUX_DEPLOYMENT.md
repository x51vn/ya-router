# Linux MVP deployment

This is the supported MVP1 deployment path: a single `systemd` service on one
Linux host. It installs the daemon under the `ya-router` service name, keeps all
mutable state in `/var/lib/ya-router`, and exposes the Control API only through
the private `/run/ya-router/control.sock` Unix socket.

The data plane listens on `127.0.0.1:7071` by default. Keep it loopback-only
unless `YA_ROUTER_API_KEY` is set and a separate TLS-terminating reverse proxy
is deliberately configured.

## Build artifacts

Build all Linux/amd64 MVP artifacts from one source revision:

```bash
VERSION="$(git describe --always --dirty)" make release-linux
cd dist/linux-amd64
sha256sum -c checksums.txt
cat build-info.txt
```

The directory contains `ya-routerd`, `ya`, the compatibility `ya-router`,
`checksums.txt`, and `build-info.txt`. `build-info.txt` records the source
commit and version used for every binary. The manual GitHub Actions workflow
uploads this same directory as the `ya-router-linux-amd64` artifact.

## Install and start

Copy the verified artifact directory and the repository's `packaging/` files
to the Linux host, then run:

```bash
getent passwd ya-router >/dev/null || \
  sudo useradd --system \
    --home-dir /var/lib/ya-router \
    --shell /usr/sbin/nologin \
    ya-router

sudo install -d -o ya-router -g ya-router -m 0700 /var/lib/ya-router
sudo install -d -o root -g ya-router -m 0750 /etc/ya-router
sudo install -m 0755 ya-routerd ya /usr/local/bin/
sudo install -m 0755 ya-router /usr/local/bin/ya-router
sudo install -m 0644 packaging/systemd/ya-router.service /etc/systemd/system/ya-router.service
sudo install -m 0640 -o root -g ya-router packaging/systemd/ya-router.env.example /etc/ya-router/ya-router.env
sudo systemctl daemon-reload
sudo systemctl enable --now ya-router
sudo systemctl status ya-router
```

Edit `/etc/ya-router/ya-router.env` only with a privileged editor. It is the
supported place for optional process credentials such as `OPENAI_API_KEY` and
`KILO_API_KEY`; it must not be world-readable. The unit does not accept
credentials as command-line arguments.

The service unit exports `YA_ROUTER_CONTROL_SOCKET` to the daemon process, but
an interactive operator shell does not inherit systemd service environment
variables. Always provide the private socket explicitly when running `ya`.

Open the dashboard:

```bash
sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya
```

Scriptable commands:

```bash
sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya providers

sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya routing

sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya operations
```

The first command opens the keyboard-driven dashboard. Scriptable commands
remain available without a terminal. The service's private socket is not
published on TCP.

### Optional: group access for a trusted operator group

Running every `ya` command as `sudo -u ya-router` is the default, owner-only
posture. An operator group can be granted socket access instead by adding
`YA_ROUTER_CONTROL_SOCKET_GROUP` to `/etc/ya-router/ya-router.env` and adding
members to that group:

```bash
sudo groupadd --system ya-router-ops
sudo usermod -aG ya-router-ops "$USER"
echo 'YA_ROUTER_CONTROL_SOCKET_GROUP=ya-router-ops' | \
  sudo tee -a /etc/ya-router/ya-router.env
sudo systemctl restart ya-router
```

Start a new login session to pick up the group membership, then run `ya`
without `sudo -u ya-router`:

```bash
env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock /usr/local/bin/ya providers
```

`YA_ROUTER_CONTROL_SOCKET_MODE` (default `0600`) can widen the file mode in the
same file if the group also needs write access to the socket; the daemon fails
to start if the configured group cannot be resolved.

## Provider and routing walkthrough

Open the dashboard as `ya-router`, press `a`, select a provider with the arrow
keys, and use the action keys shown in the footer:

1. Select GitHub Copilot and press `c` to start device authentication. Complete
   the displayed browser verification flow; the operation remains durable if
   the dashboard disconnects.
2. Select Codex and press `c` for ChatGPT device authentication, or press `p`
   to enter a masked OpenAI API key and start API-key verification.
3. Select Kilo and press `c` for anonymous free-model mode, or press `p` for a
   masked API-key setup.
4. Press `m` to refresh catalogs. The main screen shows authenticated/ready
   provider state, credential source, configured `thiendu` candidates, selected
   target per capability, skip reasons, and cooldowns.

No dashboard action edits service files or invokes systemd. Provider enable or
disable uses the daemon's expected configuration revision and asks for
confirmation; stale changes reload state instead of overwriting it.

Set `YA_ROUTER_API_KEY` in `/etc/ya-router/ya-router.env` and restart the
service before requiring inbound data-plane authentication. In a separate
operator shell, set the same value only to form the request header, then use an
ordinary OpenAI-style request against the loopback listener:

```bash
export YA_ROUTER_API_KEY='set-a-strong-service-key'

curl --fail http://127.0.0.1:7071/v1/models \
  -H "Authorization: Bearer $YA_ROUTER_API_KEY"

curl --fail http://127.0.0.1:7071/v1/chat/completions \
  -H "Authorization: Bearer $YA_ROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{"model":"thiendu","messages":[{"role":"user","content":"hello"}]}'

curl --no-buffer http://127.0.0.1:7071/v1/responses \
  -H "Authorization: Bearer $YA_ROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{"model":"thiendu","input":"hello","stream":true}'
```

The external response model remains `thiendu`. A selected target error is
returned once and is never replayed to another provider. A later request can
skip a rate-limited or transiently failing candidate during its bounded
cooldown; observe that state with `ya routing` or the dashboard. After the
cooldown expires, the preferred eligible target can be selected again.

## Health and normal operation

```bash
curl --fail http://127.0.0.1:7071/health
curl --fail http://127.0.0.1:7071/health/ready

sudo -u ya-router -H \
  curl --unix-socket /run/ya-router/control.sock \
  http://unix/control/v1/meta
```

Run the Control API command as `root` or `ya-router`; ordinary users cannot
read the owner-only socket. `systemctl stop ya-router` sends `SIGTERM` and waits
up to 45 seconds for request draining before systemd escalates.

## Upgrade, rollback, backup, and restore

Before replacing a binary, stop the service and archive its full state
directory. The directory contains the revisioned config, managed secrets, and
durable operation records.

```bash
sudo systemctl stop ya-router
sudo install -d -m 0700 /var/backups/ya-router
sudo tar -C /var/lib \
  -czf "/var/backups/ya-router/ya-router-$(date +%F-%H%M%S).tar.gz" \
  ya-router

sudo install -m 0755 ya-routerd ya /usr/local/bin/
sudo install -m 0755 ya-router /usr/local/bin/ya-router
sudo systemctl start ya-router

sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya meta
```

Rollback replaces only the binaries with the previously verified artifact and
restarts the service; leave `/var/lib/ya-router` in place. To restore a backup,
stop the service, extract the archive under `/var/lib`, ensure
`/var/lib/ya-router` is owned by `ya-router:ya-router` with mode `0700`, then
start the service and check `ya meta`, `ya operations`, and `/health/ready`.

The normal restart, upgrade, rollback, and restore paths preserve the state
directory. Do not copy individual secret or operation files while the daemon is
running.

After any restart, confirm recovery before sending traffic:

```bash
sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya providers

sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya operations

sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya routing

curl --fail http://127.0.0.1:7071/health/ready
```
