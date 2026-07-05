# Деплой лицензионного сервера через GitLab CI

Полный путь от пустого проекта на gitlab.com до работающего сервера лицензий
(`internal/licensesrv`) и релизов CLI/GUI. Пайплайн описан в `.gitlab-ci.yml`,
провижининг сервера — в `deploy/ansible/`. Общая архитектура лицензирования —
[LICENSATION.md](../LICENSATION.md) (часть A).

Основной способ деплоя — **Docker Compose** на одной VPS (`deploy/compose/`):
контейнер `server` (API лицензий) за Caddy с авто-HTTPS. Ansible ставит
Docker Engine + compose plugin (`deploy_mode: compose`, по умолчанию), а CI
джоба `deploy:compose` поднимает стек через `docker compose up -d`. k3s/Helm
(`deploy/helm/`) остаются доступны через `deploy_mode: k3s` для тех, у кого
уже есть Kubernetes; этот документ описывает основной (compose) путь, детали
самого compose-деплоя — в [deploy/compose/README.md](../deploy/compose/README.md),
их здесь не дублируем.

## Предпосылки

- Проект уже существует на gitlab.com, и `git remote` смотрит на него.
- Ветка `main` защищена (Settings → Repository → Protected branches).

> **Обязательно: Settings → Repository → Protected tags → добавь wildcard-правило
> `v*`** (роль, которой разрешено создавать теги, — как минимум Maintainers).
> Это самая частая ошибка при переносе: без protected-tag правила пайплайны,
> запущенные тегом `vX.Y.Z`, **не увидят** protected CI/CD-переменные
> (`LICENSE_PRIVATE_KEY`, `LICENSE_PUBKEY`, `CODESIGN_IDENTITY`, `AC_*` и т.д.),
> и джобы `deploy:compose`/`release:*` упадут на пустых значениях без явной
> ошибки про права — просто получат пустую строку.

## Шаг 1 — ключи лицензий

```sh
make build-server                 # → bin/singctl-server (Makefile: BINARY=singctl)
bin/singctl-server keygen
```

Печатает пару `LICENSE_PUBKEY=…` (публичный, не секрет, коммитить безопасно) и
`LICENSE_PRIVATE_KEY=…` (приватный — только в CI/CD-переменные и на сервер,
**никогда не коммитить**). Сохрани обе строки — они понадобятся в шаге 4.

Заодно реши, на каком адресе будет жить сервер лицензий (домен с авто-HTTPS
или голый IP по HTTP) — этот же адрес пойдёт в две разные CI/CD-переменные на
шаге 4: `LICENSE_SITE_ADDRESS` (как сервер сам себя раздаёт) и
`LICENSE_SERVER_URL` (куда стучится собранный клиент).

## Шаг 2 — GitLab UI: раннеры и deploy token

1. **Project runner для деплоя** — Settings → CI/CD → Runners → **New project
   runner**:
   - tags: `singctl-deploy`
   - Protected: **on**
   - Run untagged jobs: **off**

   После создания GitLab выдаёт токен вида `glrt-…` — он понадобится в шаге 3
   для `bootstrap.sh` (это runner authentication token, не Deploy token из
   следующего пункта).

2. **Deploy token** для приватного Container Registry — нужен, только если
   деплой идёт в режиме `deploy_mode: k3s`. Settings → Repository → Deploy
   tokens → New deploy token, scope **`read_registry`**. Логин/пароль этого
   токена уходят в `/etc/rancher/k3s/registries.yaml` на сервере, чтобы k3s
   мог тянуть приватный образ `$CI_REGISTRY_IMAGE` без `imagePullSecrets`.
   В режиме по умолчанию (`deploy_mode: compose`) этот шаг **не нужен**:
   джоба `deploy:compose` сама делает `docker login` встроенными
   `CI_REGISTRY_USER`/`CI_REGISTRY_PASSWORD` (GitLab предоставляет их
   автоматически, без настройки).

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

Плейбук (идемпотентен, безопасно перезапускать) ветвится по переменной
`deploy_mode` (`deploy/ansible/group_vars/all.yml`, по умолчанию `compose`;
переопределить — `./bootstrap.sh -e deploy_mode=k3s ...`):

- **Хардненинг (всегда):** UFW (deny incoming, allow только SSH-порты +
  80/443), `fail2ban` (jail на sshd, кастомный порт), SSH drop-in
  (`/etc/ssh/sshd_config.d/99-singctl.conf`: кастомный `Port`, без root-логина,
  без пароля), `unattended-upgrades`.
- **`deploy_mode: compose` (по умолчанию):** ставит Docker Engine + compose
  plugin (официальный apt-репозиторий Docker) и явно открывает 80/443 в UFW
  под Caddy. Если задан `gitlab_runner_token` — ставит `gitlab-runner` (shell
  executor) и добавляет его в группу `docker`, чтобы джоба `deploy:compose`
  могла запускать `docker`/`docker compose` без sudo.
- **`deploy_mode: k3s`:** ставит **k3s** (single-node, traefik ingress
  включён) + **helm** вместо Docker. Если заданы deploy-token логин/пароль —
  пишет `/etc/rancher/k3s/registries.yaml` с кредами для
  `registry.gitlab.com` и перезапускает k3s. Если задан
  `gitlab_runner_token` — копирует `/etc/rancher/k3s/k3s.yaml` в
  `~/.kube/config` пользователя `gitlab-runner`, чтобы helm/kubectl работали
  в CI-джобах без лишней настройки `KUBECONFIG` (для этого пути см.
  `deploy/helm/`).
- В обоих режимах, если задан `gitlab_runner_token` — регистрирует
  `gitlab-runner` на `gitlab_url` (по умолчанию `https://gitlab.com`) с этим
  токеном (тег/protected/untagged уже зафиксированы в UI при создании раннера
  в шаге 2.1 — `gitlab-runner register` их не переопределяет).

## Шаг 4 — CI/CD переменные

Settings → CI/CD → Variables. Все — Protected (видны только защищённым веткам
и protected-тегам — отсюда важность шага с protected tags выше).

| Переменная | Формат / пример | Masked | Protected | Использует |
|---|---|---|---|---|
| `LICENSE_SITE_ADDRESS` | `license.example.com`, или пусто для HTTP | да, если задан | да | `deploy:compose` → адрес, на котором Caddy раздаёт сервер (домен = авто-HTTPS; пусто = голый HTTP на `:80`). Заменяет старый Helm-only `LICENSE_HOST`/`ingress.host` |
| `LICENSE_SERVER_URL` | `https://license.example.com` или `http://<ip>` | нет (не секрет) | да | `release:macos` → зашивается в CLI/GUI как дефолтный сервер лицензий (`SINGCTL_LICENSE_SERVER` переопределяет в рантайме) |
| `LICENSE_PRIVATE_KEY` | base64 Ed25519 (из шага 1) | да | да | `deploy:compose` → `.env` сервера (подпись выданных лицензий) |
| `LICENSE_ADMIN_TOKEN` | `openssl rand -hex 32` | да | да | `deploy:compose` → `.env` сервера (bearer для `/v1/admin/*`) |
| `LICENSE_WEBHOOK_SECRET` | HMAC-секрет платёжного вебхука | да | да | `deploy:compose` → `.env` сервера; пусто = вебхук выключен |
| `LICENSE_DEFAULT_TTL_DAYS` | целое число дней, `0` = бессрочно | нет | да | `deploy:compose` → `.env` сервера, дефолтный срок действия выдаваемых лицензий |
| `LICENSE_PUBKEY` | base64 Ed25519 (из шага 1) | нет (не секрет) | да | `release:macos` → встраивается в CLI/GUI |
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

`LICENSE_SITE_ADDRESS` и `LICENSE_SERVER_URL` — две стороны одного адреса:
первая говорит серверу (Caddy), как себя раздавать, вторая — говорит
собранному клиенту, куда стучаться. Они должны указывать на один и тот же
эндпоинт (например, `LICENSE_SITE_ADDRESS=license.example.com` +
`LICENSE_SERVER_URL=https://license.example.com`, либо для голого IP —
`LICENSE_SITE_ADDRESS=` (пусто) + `LICENSE_SERVER_URL=http://<ip>`).

Отдельно:

- Встроенные `CI_REGISTRY*` / `CI_JOB_TOKEN` — предоставляются GitLab
  автоматически, никакой настройки не требуют (в т.ч. `docker login` в
  `deploy:compose` использует `CI_REGISTRY_USER`/`CI_REGISTRY_PASSWORD`).
- `gitlab_runner_token` / `gitlab_deploy_token_user` / `gitlab_deploy_token_pass`
  / `deploy_mode` — это **входные параметры Ansible** (шаг 3), не CI/CD-
  переменные. Их не нужно заводить в Settings → CI/CD → Variables.

## Шаг 5 — деплой

```sh
git push origin main
```

Запускает пайплайн `test:go` → `build:image` → `deploy:compose`:

- `test:go` — `go vet`/`go test`/сборка CLI.
- `build:image` — собирает `deploy/server.Dockerfile`, пушит
  `$CI_REGISTRY_IMAGE:$CI_COMMIT_SHA` (и `:latest` на `main`) в GitLab
  Container Registry.
- `deploy:compose` (раннер `singctl-deploy`) — пишет `deploy/compose/.env` из
  переменных шага 4 (`IMAGE`/`TAG` = `$CI_REGISTRY_IMAGE`/`$CI_COMMIT_SHA`),
  логинится в registry и гоняет
  `docker compose -f deploy/compose/docker-compose.yml pull && ... up -d`.
  Подробности стека (Caddy, том с `licenses.json`, `init-perms`) — в
  [deploy/compose/README.md](../deploy/compose/README.md).

Проверка на сервере:

```sh
ssh remote-singctl-server
cd deploy/compose && docker compose ps
curl -fsS https://<LICENSE_SITE_ADDRESS>/healthz   # или http://<ip>/healthz, если LICENSE_SITE_ADDRESS пуст
```

Выдача лицензии через админ-API (эндпоинты — `internal/licensesrv/server.go`):

```sh
curl -fsS -XPOST https://<LICENSE_SITE_ADDRESS>/v1/admin/issue \
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

- `release:macos` — **ручная** джоба (`when: manual`, `allow_failure: true`) на
  раннере с тегом `macos`; нужно нажать ▶ в UI пайплайна, и раннер должен быть
  онлайн. Собирает CLI, нативное macOS-приложение (`macos/Singctl/`) и
  `.pkg`/`.dmg` (подробности сборки — [packaging/README.md](../packaging/README.md)),
  кладёт их в артефакты джобы.
- `release:publish` — ждёт (опционально) `release:macos`, заливает файлы в
  Generic Package Registry проекта
  (`.../packages/generic/singctl/$CI_COMMIT_TAG/…`) и создаёт GitLab Release
  со ссылками на них.

`release:macos` собирает через `make build-macos` и `make app-macos` с
`LICENSE_PUBKEY="$LICENSE_PUBKEY" LICENSE_SERVER_URL="$LICENSE_SERVER_URL"`.
Без CI/CD-переменных `LICENSE_SERVER_URL` и `LICENSE_PUBKEY` (шаг 4) релизная
сборка выходит с пустым дефолтным сервером — активация лицензии сработает
только если её потом явно указать через `SINGCTL_LICENSE_SERVER` в рантайме;
для «из коробки» рабочей активации обе переменные должны быть выставлены до
тега.

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

Каждый push в `main` пересобирает образ и передеплоивает стек
(`deploy:compose`: `docker compose pull && docker compose up -d` с новым
`TAG=$CI_COMMIT_SHA` в `.env`). Откат — задеплоить старый образ вручную на
сервере (`deploy:compose` перезаписывает `.env` из CI-переменных при каждом
запуске, поэтому «нативного» `helm rollback` тут нет):

```sh
ssh remote-singctl-server
cd deploy/compose
sed -i 's/^TAG=.*/TAG=<предыдущий-sha-или-тег>/' .env
docker compose pull && docker compose up -d
```
