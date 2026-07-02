# Деплой лицензионного сервера через GitLab CI

Полный путь от пустого проекта на gitlab.com до работающего сервера лицензий
(`internal/licensesrv`) и релизов CLI/GUI. Пайплайн описан в `.gitlab-ci.yml`,
провижининг сервера — в `deploy/ansible/`. Общая архитектура лицензирования —
[LICENSATION.md](../LICENSATION.md) (часть A).

## Предпосылки

- Проект уже существует на gitlab.com, и `git remote` смотрит на него.
- Ветка `main` защищена (Settings → Repository → Protected branches).

> **Обязательно: Settings → Repository → Protected tags → добавь wildcard-правило
> `v*`** (роль, которой разрешено создавать теги, — как минимум Maintainers).
> Это самая частая ошибка при переносе: без protected-tag правила пайплайны,
> запущенные тегом `vX.Y.Z`, **не увидят** protected CI/CD-переменные
> (`LICENSE_PRIVATE_KEY`, `LICENSE_PUBKEY`, `CODESIGN_IDENTITY`, `AC_*` и т.д.),
> и джобы `deploy:helm`/`release:*` упадут на пустых значениях без явной ошибки
> про права — просто получат пустую строку.

## Шаг 1 — ключи лицензий

```sh
make build-server                 # → bin/singctl-server (Makefile: BINARY=singctl)
bin/singctl-server keygen
```

Печатает пару `LICENSE_PUBKEY=…` (публичный, не секрет, коммитить безопасно) и
`LICENSE_PRIVATE_KEY=…` (приватный — только в CI/CD-переменные и на сервер,
**никогда не коммитить**). Сохрани обе строки — они понадобятся в шаге 4.

## Шаг 2 — GitLab UI: раннеры и deploy token

1. **Project runner для деплоя** — Settings → CI/CD → Runners → **New project
   runner**:
   - tags: `singctl-deploy`
   - Protected: **on**
   - Run untagged jobs: **off**

   После создания GitLab выдаёт токен вида `glrt-…` — он понадобится в шаге 3
   для `bootstrap.sh` (это runner authentication token, не Deploy token из
   следующего пункта).

2. **Deploy token** для приватного Container Registry — Settings → Repository →
   Deploy tokens → New deploy token, scope **`read_registry`**. Логин/пароль
   этого токена тоже понадобятся `bootstrap.sh` — они уходят в
   `/etc/rancher/k3s/registries.yaml` на сервере, чтобы k3s мог тянуть
   приватный образ `$CI_REGISTRY_IMAGE` без `imagePullSecrets`.

## Шаг 3 — сервер (Ansible)

Нужен свежий Debian/Ubuntu хост с root/sudo-доступом по паролю.

```sh
cd deploy/ansible
./bootstrap.sh
```

Скрипт интерактивно спрашивает:

- `Server host/IP`, `Login (sudo-capable user)`, `Password`
- `Initial SSH port [22]` и `Custom SSH port to set [2222]`
- `GitLab runner authentication token (glrt-..., empty to skip)` — токен из
  шага 2.1
- `GitLab deploy token username (empty to skip)` / `GitLab deploy token secret
  (empty to skip)` — логин/пароль из шага 2.2

Дальше без диалога: генерирует `~/.ssh/singctl_server` (если его ещё нет),
ставит ключ на сервер через `ssh-copy-id`, прописывает алиас
`remote-singctl-server` в `~/.ssh/config` и прогоняет `playbook.yml`.

Плейбук (идемпотентен, безопасно перезапускать):

- **Хардненинг:** UFW (deny incoming, allow только SSH-порты + 80/443),
  `fail2ban` (jail на sshd, кастомный порт), SSH drop-in
  (`/etc/ssh/sshd_config.d/99-singctl.conf`: кастомный `Port`, без root-логина,
  без пароля), `unattended-upgrades`.
- **k3s** (single-node, traefik ingress включён) + **helm**.
- Если заданы deploy-token логин/пароль — пишет
  `/etc/rancher/k3s/registries.yaml` с кредами для `registry.gitlab.com` и
  перезапускает k3s.
- Если задан `gitlab_runner_token` — ставит `gitlab-runner` (shell executor),
  регистрирует его на `gitlab_url` (по умолчанию `https://gitlab.com`) с этим
  токеном (тег/protected/untagged уже зафиксированы в UI при создании раннера
  в шаге 2.1 — `gitlab-runner register` их не переопределяет) и копирует
  `/etc/rancher/k3s/k3s.yaml` в `~/.kube/config` пользователя `gitlab-runner`,
  чтобы джоба `deploy:helm` могла запускать `helm`/`kubectl` без лишней
  настройки `KUBECONFIG`.

## Шаг 4 — CI/CD переменные

Settings → CI/CD → Variables. Все — Protected (видны только защищённым веткам
и protected-тегам — отсюда важность шага с protected tags выше).

| Переменная | Формат / пример | Masked | Protected | Использует |
|---|---|---|---|---|
| `LICENSE_HOST` | `license.example.com` | да | да | `deploy:helm` → `ingress.host` |
| `LICENSE_PRIVATE_KEY` | base64 Ed25519 (из шага 1) | да | да | `deploy:helm` → Helm-секрет |
| `LICENSE_ADMIN_TOKEN` | `openssl rand -hex 32` | да | да | `deploy:helm` → Helm-секрет (bearer для `/v1/admin/*`) |
| `LICENSE_WEBHOOK_SECRET` | HMAC-секрет платёжного вебхука | да | да | `deploy:helm` → Helm-секрет; пусто = вебхук выключен |
| `LICENSE_PUBKEY` | base64 Ed25519 (из шага 1) | нет (не секрет) | да | `release:linux`/`release:macos` → встраивается в CLI/GUI |
| `CODESIGN_IDENTITY` | `Developer ID Application: <Name> (S3UCF4USYC)` | нет* | да | `release:macos` |
| `INSTALLER_IDENTITY` | `Developer ID Installer: <Name> (S3UCF4USYC)` | нет* | да | `release:macos` |
| `AC_APPLE_ID` | email аккаунта Apple Developer | нет | да | `release:macos` |
| `AC_PASSWORD` | app-specific пароль | да | да | `release:macos` |
| `AC_TEAM_ID` | `S3UCF4USYC` | нет | да | `release:macos` |
| `APPLE_CERT_P12` (опц.) | base64 .p12 сертификата | да | да | `release:macos`, только если сертификаты не в login keychain Mac-раннера |
| `APPLE_CERT_PASSWORD` (опц.) | пароль от .p12 | да | да | `release:macos`, вместе с предыдущей |

\* GitLab не даёт маскировать значения с пробелами — `CODESIGN_IDENTITY` и
`INSTALLER_IDENTITY` содержат пробелы, поэтому маскировка недоступна; их
секретность обеспечивает только Protected.

Отдельно:

- Встроенные `CI_REGISTRY*` / `CI_JOB_TOKEN` — предоставляются GitLab
  автоматически, никакой настройки не требуют.
- `gitlab_runner_token` / `gitlab_deploy_token_user` / `gitlab_deploy_token_pass`
  — это **входные параметры Ansible** (шаг 3), не CI/CD-переменные. Их не нужно
  заводить в Settings → CI/CD → Variables.

## Шаг 5 — деплой

```sh
git push origin main
```

Запускает пайплайн `test → build:image → deploy:helm`:

- `test:go` / `test:gui` — `go vet`/`go test`/сборка CLI, фронтенд + Go-тесты GUI.
- `build:image` — собирает `deploy/server.Dockerfile`, пушит
  `$CI_REGISTRY_IMAGE:$CI_COMMIT_SHA` (и `:latest` на `main`) в GitLab
  Container Registry.
- `deploy:helm` (раннер `singctl-deploy`) — `helm upgrade --install
  singctl-license deploy/helm/singctl-license --namespace singctl
  --create-namespace` с `image.repository`/`image.tag` и секретами из шага 4.

Проверка на сервере:

```sh
ssh remote-singctl-server
sudo kubectl -n singctl get pods
curl -fsS https://<LICENSE_HOST>/healthz
```

Выдача лицензии через админ-API (эндпоинты — `internal/licensesrv/server.go`):

```sh
curl -fsS -XPOST https://<LICENSE_HOST>/v1/admin/issue \
  -H "Authorization: Bearer <LICENSE_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"subject":"user@example.com","days":365}'
```

Другие маршруты: `POST /v1/admin/revoke`, `GET /v1/admin/licenses` (тоже под
bearer), `GET /v1/status`, `GET /healthz`, `POST /v1/webhook/payment`.

## Шаг 6 — релизы

```sh
git tag v1.2.3
git push --tags
```

- `release:linux` — запускается автоматически, собирает CLI/GUI и
  `.deb`/`.rpm`/`.AppImage` (подробности сборки — [packaging/README.md](../packaging/README.md)),
  кладёт их в артефакты джобы.
- `release:macos` — **ручная** джоба (`when: manual`, `allow_failure: true`) на
  раннере с тегом `macos`; нужно нажать ▶ в UI пайплайна, и раннер должен быть
  онлайн.
- `release:publish` — ждёт `release:linux` и (опционально) `release:macos`,
  заливает файлы в Generic Package Registry проекта
  (`.../packages/generic/singctl/$CI_COMMIT_TAG/…`) и создаёт GitLab Release
  со ссылками на них.

### Регистрация Mac-раннера (для `release:macos`)

1. Settings → CI/CD → Runners → New project runner: tag `macos`, Protected on.
2. На самом Mac:
   ```sh
   brew install gitlab-runner
   gitlab-runner register --url https://gitlab.com --token glrt-… --executor shell
   brew services start gitlab-runner
   ```
   Identity-сертификаты Developer ID и профиль `notarytool` должны уже лежать
   в login keychain пользователя, под которым запущен `gitlab-runner` (см.
   комментарий в `.gitlab-ci.yml` про `release:macos`).

Альтернатива без Mac-раннера: собрать пакет локально
(`make pkg-macos …`, см. [packaging/README.md](../packaging/README.md)) и
приложить файлы к GitLab Release вручную.

## Обновление сервера

Каждый push в `main` пересобирает образ и переустанавливает Helm-релиз
(`deploy:helm` использует `--wait`, так что пайплайн падает, если новый под не
поднялся). Откат:

```sh
ssh remote-singctl-server
sudo helm rollback singctl-license
```
