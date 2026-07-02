# Provisioning the singctl license server

One-command bootstrap of a fresh Debian/Ubuntu box: SSH key + hardening +
Docker Engine/compose (default) or k3s/helm + (optionally) a self-hosted
GitLab Runner.

## 1. Bootstrap

```sh
cd deploy/ansible
./bootstrap.sh
```

It prompts for the server host, login, password, a custom SSH port, and
(optionally) GitLab integration values, then:
1. generates `~/.ssh/singctl_server` (if absent) and installs it on the server,
2. writes a `~/.ssh/config` entry so you can later just `ssh remote-singctl-server`
   (it points at the custom port the playbook switches SSH to),
3. runs `playbook.yml` (hardening + workload runtime + optional gitlab-runner,
   see below).

Requirements locally: `ssh`, `ssh-keygen`, `ssh-copy-id`, `sshpass`, `ansible`,
plus the `community.general` collection (`ansible-galaxy collection install
community.general`).

To also register the deploy runner and the registry pull creds in the same run,
answer the extra prompts (`GitLab runner authentication token`, `GitLab deploy
token username`, `GitLab deploy token secret`) — or pass them non-interactively
as extra vars:

```sh
./bootstrap.sh -e gitlab_runner_token=glrt-... \
               -e gitlab_deploy_token_user=<user> \
               -e gitlab_deploy_token_pass=<token>
```

Get these from the GitLab UI first — see
**[docs/deploy-gitlab.md](../../docs/deploy-gitlab.md)** for exactly where.

## 2. What the playbook does

`deploy_mode` (`group_vars/all.yml`, default `compose`; override with
`-e deploy_mode=k3s`) picks which workload runtime gets installed on top of
the hardening below.

- **Hardening (always):** UFW (deny incoming; allow the SSH ports + 80/443),
  fail2ban (sshd jail on the custom port), SSH drop-in (`Port`, no root, no
  password auth), `unattended-upgrades`.
- **`deploy_mode: compose` (default):** Docker Engine + the compose plugin
  (official Docker apt repo) — this is what `deploy/compose/` runs on. UFW
  80/443 is asserted explicitly for Caddy. If a `gitlab_runner_token` is
  given, the installed `gitlab-runner` user is also added to the `docker`
  group so the `deploy:compose` CI job can run `docker`/`docker compose`
  without sudo.
- **`deploy_mode: k3s`:** k3s (single node, traefik ingress included) + helm
  instead of Docker — this is what `deploy/helm/` targets. Optional
  `/etc/rancher/k3s/registries.yaml` with a GitLab **deploy token** (scope
  `read_registry`) so k3s can pull the private image from
  `registry.gitlab.com` without `imagePullSecrets`. If a `gitlab_runner_token`
  is given, `~/.kube/config` for the `gitlab-runner` user is copied from the
  k3s kubeconfig so `helm`/`kubectl` work out of the box in CI jobs.
- Optional **self-hosted GitLab Runner** (shell executor, project runner
  tagged `singctl-deploy`) as a service in either mode — this is what the
  `deploy:compose` (or, in k3s mode, `helm upgrade`-driven) job in
  `.gitlab-ci.yml` runs on.

## 3. Deploy

After the runner is up, set the CI/CD variables (Settings → CI/CD → Variables)
and push to `main` (or tag `vX.Y.Z`) — the GitLab CI pipeline (`.gitlab-ci.yml`)
builds the image, pushes it to the GitLab Container Registry, and the
`deploy:compose` job runs `docker compose up -d` on the runner (compose mode;
k3s mode instead `helm upgrade --install`s `deploy/helm/singctl-license`,
namespace `singctl`).

For the full variable list, values/formats, and the complete step-by-step
(including registering the runners in the GitLab UI, protected tags, and
issuing licenses), see **[docs/deploy-gitlab.md](../../docs/deploy-gitlab.md)**
and, for the compose stack itself, **[deploy/compose/README.md](../compose/README.md)**.
Generate the keypair first with `bin/singctl-server keygen` (private →
`LICENSE_PRIVATE_KEY` variable; public → `LICENSE_PUBKEY` variable + the CLI
build).

## Notes

- SSH moves to the custom port at the end of the run; reconnect via
  `ssh remote-singctl-server`. The initial port stays allowed in UFW so the run
  never locks itself out — tighten it later if desired.
- Re-running the playbook is safe (idempotent).
