# singctl license server — Docker Compose deployment

Single-VPS deployment: one `server` container (the license API) fronted by
Caddy (automatic HTTPS). No Kubernetes required. This is the primary,
recommended deployment path — see `deploy/helm/` only if you're already
running k3s (`deploy_mode: k3s` in the Ansible provisioning).

**The license store is a single atomic JSON file on a local volume — run
exactly ONE `server` container, always. Do not scale this service; a second
replica would silently corrupt or race on the store.**

## Bring-up

Requirements on the host: Docker Engine + the compose plugin (the Ansible
playbook in `deploy/ansible/` installs both when `deploy_mode: compose`).

```sh
cd deploy/compose
cp .env.example .env
```

Fill in `.env`:

- `LICENSE_SITE_ADDRESS` — your domain (e.g. `license.example.com`) for
  automatic HTTPS, or leave empty for plain HTTP on `:80`.
- `LICENSE_PRIVATE_KEY` — generate an Ed25519 keypair:
  ```sh
  docker run --rm --entrypoint /server registry.gitlab.com/GROUP/singctl-proxy:latest keygen
  ```
  Put the private key in `.env`; keep the public key for CLI builds
  (`LICENSE_PUBKEY`) — it never goes on the server.
- `LICENSE_ADMIN_TOKEN` — a random bearer token for `/v1/admin/*`:
  ```sh
  openssl rand -hex 32
  ```
- `LICENSE_WEBHOOK_SECRET` — HMAC secret for the payment webhook, or leave
  empty to disable it.
- `IMAGE` / `TAG` — which image to pull (defaults to the GitLab registry
  path + `latest`).

Then:

```sh
docker compose up -d
```

Verify:

```sh
curl -fsS http://localhost/healthz     # or https://<LICENSE_SITE_ADDRESS>/healthz
```

## Notes

- `init-perms` is a one-shot container that `chown`s the data volume to the
  distroless image's nonroot uid/gid (65532) before `server` starts, since
  the server image has no shell to do this itself. It exits after running;
  `server` waits for it via `depends_on: service_completed_successfully`.
- There is no in-container healthcheck on `server` — the distroless base has
  no shell/curl/wget to run one. Caddy's reverse proxy (and its own health
  as an HTTP frontend) is the effective health signal; `docker compose ps`
  and `restart: unless-stopped` handle recovery.
- Data lives in the named volume `singctl-data` (mounted at `/data` in the
  container, containing `licenses.json`). Back it up before upgrades:
  ```sh
  docker run --rm -v singctl-data:/data -v "$PWD":/backup busybox \
    tar czf /backup/singctl-data.tgz -C /data .
  ```
- To upgrade: pull the new image tag into `.env` (`TAG=...`), then
  `docker compose pull && docker compose up -d`.
