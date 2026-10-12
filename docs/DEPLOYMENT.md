# Server deployment

The server profile uses the same binary and schema as desktop with a separate workspace. This repository provides a single-host Docker/Compose/Caddy configuration. **Docker image build, image availability, TLS, container permissions and restart persistence have not been executed in the current verification environment.** The configuration is an implementation deliverable, not an online service or a passed deployment gate.

## Direct binary operation

Initialize a fresh private directory without opening a network service:

```bash
./flowdesk --profile server --data-dir /srv/flowdesk --init-admin --no-browser
```

Run behind a TLS reverse proxy, preserving the fixed public Host:

```bash
./flowdesk --profile server --data-dir /srv/flowdesk \
  --listen 127.0.0.1:8080 --public-origin https://support.example.com \
  --trusted-proxy-cidrs 127.0.0.1/32 --no-browser
```

Replace the sample hostname with the actual TLS name. Server requires an exact HTTPS public origin without path, userinfo, query or fragment. Secure host-only cookies, write Origin and Host checks use that origin; forwarding headers do not redefine it. Only configure trusted proxy addresses under your control. Backend ports must not be directly exposed.

Server starts in demo and creates no public default credentials. Starting a server profile against desktop data fails before schema/key writes. One workspace/application process/local filesystem only; shared network SQLite and replicas are unsupported.

## Compose preparation

Run from the source root. Set `FLOWDESK_HOST` to the real hostname, ensure it resolves to this host and that ports80/443 are available. Check that the fixed private subnet172.30.24.0/24 does not conflict; if changed, update gateway address and the exact trusted-proxy CIDR together.

```bash
export FLOWDESK_HOST=support.example.com
docker compose -f deploy/compose.yml config
docker compose -f deploy/compose.yml build app
docker compose -f deploy/compose.yml run --rm --no-deps app \
  --profile server --init-admin --no-browser
docker compose -f deploy/compose.yml up -d
```

The app has no host port publication, runs as UID/GID10001, and stores data in one named volume. Its filesystem is read-only except the volume and bounded temporary area. Caddy stores TLS state separately. Do not initialize a second workspace by replacing the volume unintentionally.

For backup/reset/recovery, first stop the app service, then use a one-shot app container against the same volume. The instance lock prevents concurrent offline operations. `docker compose down` keeps named data volumes; `down -v` removes them and must only be used when deletion is intentional.

Default build image references are Go1.27.2/Node24.19.0, Debian bookworm-slim and Caddy2.10.2-alpine. These tags are configurable but are **not digest-pinned or pull-verified here**. Before recording a reproducible container result, resolve exact image digests, preserve the resulting image ID/build args, and test the clean-room steps below.

## Live provider policy

Enable only when intended, adding `--live-enabled` and `--model-egress-hosts api.your-provider.example:443` to the app command. Then an administrator saves a matching HTTPS base URL, actual model identifier and secret in the UI. No key is supplied in Compose, source, build args or public examples.

Provider DNS addresses must be public and pinned per request; private, loopback, link-local, metadata and IPv4-mapped private addresses are rejected. Redirects and environment proxies are disabled. The application allowlist applies to model requests; it is not an OS/container firewall and does not claim complete outbound traffic isolation.

## Required clean-room checks

1. Build images with recorded digests; initialize an empty volume; verify no demo user/default password exists.
2. Confirm HTTPS login/cookie, request Origin, fixed Host and trusted-proxy address handling.
3. Complete ticket → draft → human review → requester public reply.
4. Restart without replacing the volume and confirm data/history; an interrupted model request must not repeat automatically.
5. Confirm directory/key permissions, low disk/write failure, health readiness and shutdown.
6. Stop app; run consistent backup and offline password/key recovery; preserve all paired files.
7. Check only gateway80/443 are exposed and backend remains private.

`/api/health` reports process/version/profile; `/api/ready` also checks SQL and paused storage. Container probe sets the configured public Host while reaching localhost8080. No public hosting or remote CI result is claimed by this source package.

Primary references: [Docker multi-stage builds](https://docs.docker.com/build/building/multi-stage/) and [Caddy reverse_proxy](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy).

