#!/usr/bin/env bash
# bootstrap.sh — interactive provisioning of the singctl license server.
#
# Prompts for the server address, login, password and a CUSTOM SSH port, then:
#   1. generates an SSH keypair (~/.ssh/singctl_server) if absent,
#   2. installs it on the server (ssh-copy-id over the password),
#   3. writes a ~/.ssh/config entry so `ssh remote-singctl-server` just works
#      (pointing at the custom port the playbook will switch SSH to),
#   4. runs the hardening + k3s playbook over the key.
#
# Requirements (local): ssh, ssh-keygen, ssh-copy-id, sshpass, ansible.
set -euo pipefail
cd "$(dirname "$0")"

ALIAS="remote-singctl-server"
KEY="$HOME/.ssh/singctl_server"
SSH_CONFIG="$HOME/.ssh/config"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 1; }; }
need ssh; need ssh-keygen; need ssh-copy-id; need ansible-playbook
command -v sshpass >/dev/null 2>&1 || echo "note: sshpass not found — ssh-copy-id may prompt for the password interactively"

read -rp "Server host/IP: " SERVER
read -rp "Login (sudo-capable user): " LOGIN
read -rsp "Password: " PASSWORD; echo
read -rp "Initial SSH port [22]: " INIT_PORT; INIT_PORT="${INIT_PORT:-22}"
read -rp "Custom SSH port to set [2222]: " SSH_PORT; SSH_PORT="${SSH_PORT:-2222}"

# Optional GitLab integration values (empty answer = skip / leave default).
read -rp "GitLab runner authentication token (glrt-..., empty to skip): " GITLAB_RUNNER_TOKEN
read -rp "GitLab deploy token username (empty to skip): " GITLAB_DEPLOY_TOKEN_USER
read -rsp "GitLab deploy token secret (empty to skip): " GITLAB_DEPLOY_TOKEN_PASS; echo

# 1. key
if [[ ! -f "$KEY" ]]; then
  echo "==> generating SSH key $KEY"
  ssh-keygen -t ed25519 -N "" -f "$KEY" -C "singctl-license"
fi

# 2. install the key on the server (over the password, initial port)
echo "==> installing key on $LOGIN@$SERVER:$INIT_PORT"
if command -v sshpass >/dev/null 2>&1; then
  sshpass -p "$PASSWORD" ssh-copy-id -o StrictHostKeyChecking=accept-new \
    -i "$KEY.pub" -p "$INIT_PORT" "$LOGIN@$SERVER"
else
  ssh-copy-id -o StrictHostKeyChecking=accept-new -i "$KEY.pub" -p "$INIT_PORT" "$LOGIN@$SERVER"
fi

# 3. ~/.ssh/config alias → custom port (active after the playbook moves SSH there)
echo "==> writing $SSH_CONFIG entry '$ALIAS'"
mkdir -p "$HOME/.ssh"; chmod 700 "$HOME/.ssh"
touch "$SSH_CONFIG"; chmod 600 "$SSH_CONFIG"
# Drop any previous block for this alias, then append a fresh one.
if grep -q "^Host $ALIAS\$" "$SSH_CONFIG"; then
  awk -v a="$ALIAS" '
    $1=="Host" && $2==a {skip=1; next}
    $1=="Host" && $2!=a {skip=0}
    !skip {print}
  ' "$SSH_CONFIG" > "$SSH_CONFIG.tmp" && mv "$SSH_CONFIG.tmp" "$SSH_CONFIG"
fi
cat >> "$SSH_CONFIG" <<EOF

Host $ALIAS
    HostName $SERVER
    User $LOGIN
    Port $SSH_PORT
    IdentityFile $KEY
    IdentitiesOnly yes
EOF

# 4. run the playbook over the key, on the INITIAL port (SSH not yet moved).
echo "==> running playbook (hardening + k3s)"
ansible-playbook playbook.yml \
  -i "$SERVER," \
  -u "$LOGIN" \
  --private-key "$KEY" \
  -e ansible_port="$INIT_PORT" \
  -e ssh_port="$SSH_PORT" \
  -e ansible_python_interpreter=/usr/bin/python3 \
  -e gitlab_runner_token="$GITLAB_RUNNER_TOKEN" \
  -e gitlab_deploy_token_user="$GITLAB_DEPLOY_TOKEN_USER" \
  -e gitlab_deploy_token_pass="$GITLAB_DEPLOY_TOKEN_PASS" \
  "$@"

cat <<EOF

Done. SSH now listens on port $SSH_PORT. Connect with:
    ssh $ALIAS

Next:
  1. Create the project runner in the GitLab UI (Settings → CI/CD → Runners →
     New project runner), tag "singctl-deploy", Protected=on.
  2. Set the CI/CD variables per docs/deploy-gitlab.md.
  3. Push to main to trigger the deploy pipeline.
EOF
