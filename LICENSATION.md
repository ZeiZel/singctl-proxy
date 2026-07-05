# LICENSATION — лицензирование singctl

Документ состоит из двух независимых частей:

- **Часть A — Лицензионный сервер** (офлайн-лицензии Ed25519): как поднять сервер,
  встроить публичный ключ в CLI и выдавать токены. Эпик `singctl-proxy-z33` и все
  дочерние беды закрыты — код уже в репозитории, ниже описан **порядок запуска**.
- **Часть B — Apple-модули для per-app изоляции (macOS)**: System Extension +
  контейнер-приложение, нотаризация и дистрибуция.

---

# Часть A — Лицензионный сервер (офлайн Ed25519)

Гейтинг лицензии работает **офлайн**: сервер подписывает токен своим приватным
Ed25519-ключом, а CLI проверяет его встроенным публичным ключом
(`internal/license`, `internal/licensesrv`; CLI-гейт — `internal/license/gate*.go`).
Приватный ключ **никогда** не покидает сервер; публичный безопасно коммитить и
встраивать в сборку.

## A0. Что где в коде

| Компонент | Где |
|---|---|
| Ядро лицензий (claims, sign/verify, keygen) | `internal/license` |
| HTTP-сервер + хранилище (JSON) + платёжный вебхук | `internal/licensesrv` |
| CLI сервера/админки (`serve`/`keygen`/`issue`/`revoke`) | `cmd/server` |
| Образ контейнера | `deploy/server.Dockerfile` |
| Helm-чарт | `deploy/helm/singctl-license` |
| Провижининг (SSH+хардненинг+k3s+runner) | `deploy/ansible` |
| CI/CD | `.gitlab-ci.yml` |

Сборка бинаря сервера локально: `make build-server` → `bin/singctl-server`
(чистый Go, CGO-free, без sing-box). Команды: `serve`, `keygen`, `issue`,
`revoke` (см. `bin/singctl-server help`).

## A1. Порядок запуска

### Шаг 1 — сгенерировать ключевую пару

```sh
make build-server                 # → bin/singctl-server
bin/singctl-server keygen
```

Выводит две строки:

- `LICENSE_PUBKEY=…`  — **публичный** ключ. Встраивается в CLI при сборке
  (`make build LICENSE_PUBKEY=…`, см. `Makefile`), безопасен для коммита. В CI
  это CI/CD-переменная `LICENSE_PUBKEY`, которую джоба `release:macos` из
  `.gitlab-ci.yml` подставляет в сборку.
- `LICENSE_PRIVATE_KEY=…` — **приватный** ключ. Только на сервере (CI/CD-
  переменная `LICENSE_PRIVATE_KEY` → Helm-секрет). **Никогда не коммить.**

### Шаг 2 — провижининг сервера (Ansible)

```sh
cd deploy/ansible
./bootstrap.sh
```

Интерактивно спросит host / логин / пароль / кастомный SSH-порт, а также
(опционально) GitLab-токены, затем установит SSH-ключ, пропишет алиас
`remote-singctl-server` в `~/.ssh/config` и прогонит `playbook.yml` (UFW +
fail2ban + SSH drop-in, k3s + traefik + helm). Чтобы в том же прогоне
зарегистрировать self-hosted раннер деплоя (и registry-креды для приватного
образа), ответь на дополнительные промпты скрипта:

```
GitLab runner authentication token (glrt-..., empty to skip): glrt-...
GitLab deploy token username (empty to skip): <deploy-token-user>
GitLab deploy token secret (empty to skip): <deploy-token-pass>
```

либо передай их как extra vars неинтерактивно:

```sh
./bootstrap.sh -e gitlab_runner_token=glrt-... \
               -e gitlab_deploy_token_user=<user> \
               -e gitlab_deploy_token_pass=<token>
```

`gitlab_runner_token` (вида `glrt-…`) — это **runner authentication token**
нового формата, который GitLab выдаёт один раз при создании project runner
(**Settings → CI/CD → Runners → New project runner**, тег `singctl-deploy`,
Protected=on, без untagged) — не путать с deploy token'ом ниже. Раннер держит
локальный kubeconfig + helm, поэтому API кластера наружу не выставляется.
`gitlab_deploy_token_user`/`gitlab_deploy_token_pass` — логин/пароль project
deploy token'а (**Settings → Repository → Deploy tokens**, scope
`read_registry`), они уходят в `/etc/rancher/k3s/registries.yaml` для доступа
к приватному GitLab Container Registry. Требования локально: `ssh`,
`ssh-keygen`, `ssh-copy-id`, `sshpass`, `ansible` + коллекция
`community.general`. Подробности — `deploy/ansible/README.md` и
[docs/deploy-gitlab.md](docs/deploy-gitlab.md).

### Шаг 3 — CI/CD-переменные → push → deploy

В **Settings → CI/CD → Variables** задай (полная таблица форматов/masked/
protected — в [docs/deploy-gitlab.md](docs/deploy-gitlab.md)):

| Переменная | Значение | Кто использует |
|---|---|---|
| `LICENSE_PRIVATE_KEY` | приватный ключ из шага 1 | `deploy:helm` → Helm-секрет |
| `LICENSE_ADMIN_TOKEN` | bearer-токен для `/v1/admin/*` | `deploy:helm` → Helm-секрет |
| `LICENSE_WEBHOOK_SECRET` | HMAC-секрет платёжного вебхука | `deploy:helm` → Helm-секрет |
| `LICENSE_HOST` | внешний хост (FQDN для ingress/TLS) | `deploy:helm` → `ingress.host` |
| `LICENSE_PUBKEY` | публичный ключ из шага 1 | `release:macos` → встраивание в CLI |

Затем `git push` в `main` (или тег `vX.Y.Z`) запускает пайплайн из
`.gitlab-ci.yml`: `test` → `build:image` (сборка образа, push в GitLab
Container Registry, `$CI_REGISTRY_IMAGE`) → `deploy:helm` (раннер
`singctl-deploy`, `helm upgrade --install singctl-license`, namespace
`singctl`). Тег `vX.Y.Z` дополнительно запускает `release:macos` (ручная
джоба на раннере с тегом `macos`) — сборку CLI/GUI с встроенным
`LICENSE_PUBKEY`, а `release:publish` публикует GitLab Release.
Подробный пошаговый разбор (включая создание раннеров и protected tags) —
[docs/deploy-gitlab.md](docs/deploy-gitlab.md).

### Шаг 4 — выдать лицензию

**Удалённо, через админ-API** (token в stdout-поле `token`):

```sh
curl -fsS -XPOST https://<host>/v1/admin/issue \
  -H "Authorization: Bearer <LICENSE_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"subject":"user@example.com","days":365}'
# → {"id":"…","token":"…"}
```

Поля тела: `subject` (обязательно), `days` (необязательно; `0` или меньше =
бессрочно), `features` (массив строк). Маршруты сервера:
`POST /v1/admin/issue`, `POST /v1/admin/revoke`, `GET /v1/admin/licenses`
(все под bearer-токеном), `GET /v1/status`, `GET /healthz`,
`POST /v1/webhook/payment`.

**Локально на хосте** (тот же JSON-store, без HTTP — печатает токен в stdout):

```sh
LICENSE_PRIVATE_KEY=<priv> LICENSE_DB=/data/licenses.json \
  bin/singctl-server issue --subject user@example.com --days 365
# отзыв:
LICENSE_DB=/data/licenses.json bin/singctl-server revoke --id <license-id>
```

Готовый токен пользователь вставляет в CLI (`internal/license/gate*.go` проверяет
его встроенным `LICENSE_PUBKEY` офлайн).

## A2. Замечания

- **Один источник правды по приватному ключу.** Он живёт только в секрете репо
  `LICENSE_PRIVATE_KEY` (→ Helm-секрет на сервере). Утечёт — перевыпускай пару
  (шаг 1) и пересобирай CLI с новым `LICENSE_PUBKEY` (старые токены станут
  недействительны).
- **`days:0` = бессрочная лицензия.** Дефолт сервера — 365 дней
  (`LICENSE_DEFAULT_TTL_DAYS`).
- **Платёжный вебхук** включается только при заданном `LICENSE_WEBHOOK_SECRET`
  (`GenericHMAC`), иначе `POST /v1/webhook/payment` выключен.
- **Dev-сборки без `LICENSE_PUBKEY`** не умеют проверять лицензии — для локальной
  работы используй `make build-unlicensed` (тег `unlicensed`; CI его никогда не
  публикует).
- **Хранилище** — единый JSON-файл (`LICENSE_DB`, по умолчанию `./licenses.json`);
  в кластере он на PVC (`deploy/helm/singctl-license/templates/pvc.yaml`).

---

# Часть B — Apple-модули для per-app изоляции (macOS)

Эта инструкция — последовательность действий, чтобы заработали Apple-модули,
которыми singctl изолирует трафик **отдельных приложений** на macOS: системное
расширение `NETransparentProxyProvider` (`com.singctl.proxy.netext`) + контейнер-
приложение, которое его активирует. Расширение перехватывает **весь** сетевой
стек выбранного приложения (Chromium, Node/undici, raw-сокеты), поэтому форки и
хелперы (например, extension-host Cursor) покрываются автоматически.

> **Важно: дистрибуция — Developer ID + нотаризация, НЕ App Store.**
> System Extension нельзя распространять через Mac App Store; VPN/TUN-режим
> требует root и несовместим с App-Sandbox (обязательным для Store); а голый CLI
> в Store не публикуется. Поэтому путь — подписанный Developer ID `.app`/`.pkg` +
> нотаризация, распространяемые вне Store. (Линукс-изоляция приложений работает
> без всего этого — бесплатно, через cgroup/nftables.)

## 0. Что понадобится

- **Apple Developer Program** — платное членство ($99/год). Бесплатный Apple ID
  **не подойдёт**: entitlement Network Extensions и нотаризация доступны только
  платным аккаунтам.
- **Xcode** (+ command line tools) и **XcodeGen** (`brew install xcodegen`).
- **Team ID** (10 символов). Проектный Team ID — `S3UCF4USYC` (используется по
  умолчанию в `build.sh`/`Makefile`; переопределяется через `DEVELOPMENT_TEAM=`
  для другого аккаунта). Где взять свой:
  - `security find-identity -v -p codesigning` → в скобках в конце строки;
  - или developer.apple.com/account → **Membership details** → Team ID;
  - или Xcode → Settings → Accounts → команда.
- **Developer ID Network Extension entitlement** — распространение системного
  расширения (Network Extension) с подписью **Developer ID** требует отдельного
  одобрения Apple. Запроси его как можно раньше на
  https://developer.apple.com/contact/request/network-extension (выбери Developer
  ID distribution) — рассмотрение занимает от нескольких дней до нескольких
  недель. Локальные Development-сборки работают и без него — через
  `systemextensionsctl developer on` (см. «Траблшутинг» ниже).

## 1. Идентификаторы (App IDs) и capabilities

В developer.apple.com/account → **Identifiers** создай два App ID (они должны
вкладываться: extension — суффикс к app):

| Роль | Bundle ID | Capabilities |
|---|---|---|
| Контейнер-приложение | `com.singctl.proxy` | App Groups, Network Extensions |
| System Extension | `com.singctl.proxy.netext` | App Groups, Network Extensions |

Создай **App Group** `group.com.singctl.proxy` и включи его у обоих App ID.
Этот App Group — общий контейнер, через который CLI отдаёт расширению список
приложений (`config.json`, см. `internal/netext`).

## 2. Provisioning profiles

Для каждого App ID создай профиль уровня **Developer ID** (для распространения вне
Store) с включённым `*-systemextension`-entitlement Network Extensions. На время
разработки можно начать с Development-профилей и автоподписи в Xcode.

## 3. Entitlements в репозитории

Файлы уже есть, при необходимости подставь свой Team ID / идентификаторы:
- `macos/Singctl/ProxyExtension.entitlements`
- `macos/Singctl/Singctl.entitlements`

Оба объявляют `com.apple.developer.networking.networkextension`
(`*-systemextension`) и App Group `group.com.singctl.proxy`.

**CLI `singctl` тоже подпиши с App Group entitlement** — иначе он не сможет писать
общий `config.json` (`~/Library/Group Containers/group.com.singctl.proxy/
config.json`), и per-app изоляция вернёт ошибку записи в TUI. Минимальный
entitlements-файл для CLI:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>com.apple.security.application-groups</key>
  <array><string>group.com.singctl.proxy</string></array>
</dict></plist>
```

Подпись CLI:
```sh
codesign --force --options runtime \
  --entitlements singctl.entitlements \
  --sign "Developer ID Application: <Your Name> (S3UCF4USYC)" \
  bin/singctl
```

## 4. Сборка приложения + расширения

```sh
make app-macos                       # defaults to DEVELOPMENT_TEAM=S3UCF4USYC
make app-macos DEVELOPMENT_TEAM=OTHERTEAMID   # override for another Apple account
# = macos/Singctl/build.sh: xcodegen generate + xcodebuild Release
```

Альтернатива — вручную в Xcode: `cd macos/Singctl && xcodegen
generate && open Singctl.xcodeproj`, выставить Team у обоих таргетов,
Product → Archive.

## 5. Первый запуск и одобрение

1. Запусти собранный `Singctl.app` один раз.
2. Одобри расширение: **System Settings → General → Login Items & Extensions →
   Network Extensions** (на старых macOS — Security & Privacy).
3. Разреши конфигурацию прокси, если система спросит.

Проверка статуса: `systemextensionsctl list` — у `com.singctl.proxy.netext`
должно быть состояние `[activated enabled]` (именно это проверяет
`netext.Available()`).

## 6. Нотаризация (обязательно для распространения)

```sh
# один раз сохранить креды notarytool:
xcrun notarytool store-credentials singctl-notary \
  --apple-id <you@example.com> --team-id S3UCF4USYC --password <app-specific-pwd>

# собрать .pkg/.zip с .app, затем:
xcrun notarytool submit Singctl.zip --keychain-profile singctl-notary --wait
xcrun stapler staple Singctl.app
```

`make app-macos NOTARY_PROFILE=<profile>` делает это автоматически (см.
`macos/Singctl/build.sh`).

## 7. Дистрибуция

Раздавай подписанный Developer ID + нотаризованный `.app`/`.pkg` вместе с
Developer-ID-подписанным CLI `singctl`. **Не** через App Store (см. врезку вверху).

## 8. Проверка, что изоляция работает

1. В singctl выбери приложение для изоляции (раздел «Приложения»). CLI запишет
   его bundle ID в `config.json`; расширение подхватит его (file-watch).
2. Убедись, что трафик приложения идёт через прокси:
   ```sh
   lsof -nP -p <PID процесса приложения> -i
   ```
   Соединения должны идти на `127.0.0.1:1080` (локальный SOCKS singctl), а не
   напрямую во внешний AWS/Cloudflare.

## Траблшутинг

- **`netext.Available()` = false / «расширение не одобрено»** — пройди шаг 5;
  проверь `systemextensionsctl list`.
- **Ошибка записи `config.json`** — CLI не подписан с App Group entitlement
  (шаг 3) или нет членства/контейнера.
- **`xcodebuild` падает на provisioning** — не включены capabilities Network
  Extensions/App Groups у App ID (шаг 1) или не выбран Team (шаг 4).
- **Расширение не грузится после установки** — нужна нотаризация (шаг 6) либо на
  dev-машине временно ослабить через `systemextensionsctl developer on`.
