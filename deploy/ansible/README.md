# Provisioning the singctl license server

One-command bootstrap of a fresh Debian/Ubuntu box: SSH key + hardening + k3s +
helm + (optionally) a self-hosted GitHub Actions runner.

## 1. Bootstrap

```sh
cd deploy/ansible
./bootstrap.sh
```

It prompts for the server host, login, password, and a custom SSH port, then:
1. generates `~/.ssh/singctl_server` (if absent) and installs it on the server,
2. writes a `~/.ssh/config` entry so you can later just `ssh remote-singctl-server`
   (it points at the custom port the playbook switches SSH to),
3. runs `playbook.yml` (hardening + k3s + helm).

Requirements locally: `ssh`, `ssh-keygen`, `ssh-copy-id`, `sshpass`, `ansible`,
plus the `community.general` collection (`ansible-galaxy collection install
community.general`).

To also register the deploy runner and GHCR pull creds in the same run:

```sh
./bootstrap.sh -e github_runner_url=https://github.com/OWNER/REPO \
               -e github_runner_token=<runner-token> \
               -e ghcr_user=<user> -e ghcr_token=<ghcr-PAT>
```

## 2. What the playbook does

- **Hardening:** UFW (deny incoming; allow the SSH ports + 80/443), fail2ban
  (sshd jail on the custom port), SSH drop-in (`Port`, no root, no password auth),
  `unattended-upgrades`.
- **k3s** (single node, traefik ingress included) + **helm**.
- Optional `/etc/rancher/k3s/registries.yaml` for pulling the private GHCR image.
- Optional **self-hosted runner** (labels `self-hosted,singctl`) as a service —
  this is what `.github/workflows/deploy.yml` runs `helm upgrade` on.

## 3. Deploy

After the runner is up, set the repo secrets (`LICENSE_PRIVATE_KEY`,
`LICENSE_ADMIN_TOKEN`, `LICENSE_WEBHOOK_SECRET`, `LICENSE_HOST`, `LICENSE_PUBKEY`)
and push to `main` (or tag) — the deploy workflow builds the image, pushes to
GHCR, and `helm upgrade --install`s the chart on the runner. Generate the keypair
first with `bin/singctl-server keygen` (private → `LICENSE_PRIVATE_KEY` secret;
public → `LICENSE_PUBKEY` secret + the CLI build).

## Notes

- SSH moves to the custom port at the end of the run; reconnect via
  `ssh remote-singctl-server`. The initial port stays allowed in UFW so the run
  never locks itself out — tighten it later if desired.
- Re-running the playbook is safe (idempotent).
