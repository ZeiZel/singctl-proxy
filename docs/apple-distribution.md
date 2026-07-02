# Apple-дистрибуция singctl: от Developer Program до бесплатной раздачи команде

Эта страница — сквозной путь для macOS-сборки singctl: что получить в Apple
Developer, как собрать **лицензионный**, подписанный и нотаризованный билд, и
как раздать его команде **бесплатно** (каждому — бессрочный токен). Team ID
проекта — **`S3UCF4USYC`**.

Здесь — только сводка и порядок действий; подробности по каждому шагу
вынесены в уже существующие документы, ссылки даны по месту:

- **[LICENSATION.md](../LICENSATION.md)** — часть A (лицензионный сервер,
  Ed25519, `issue`/`revoke`) и часть B (Apple-модули: App IDs, App Group,
  entitlements, нотаризация системного расширения).
- **[packaging/macos/netextension/RUNBOOK.md](../packaging/macos/netextension/RUNBOOK.md)**
  — что уже сделано в Swift-коде расширения и что остаётся доделать на Mac.
- **[packaging/README.md](../packaging/README.md)** — команды сборки `.pkg`/`.dmg`,
  переменные окружения подписи, CI-джоба `release:macos`.
- **[docs/deploy-gitlab.md](deploy-gitlab.md)** — разворачивание лицензионного
  сервера (Helm/k3s) и CI/CD-переменные.
- **[docs/macos.md](macos.md)** — как per-app изоляция выглядит и работает для
  пользователя, сосуществование с Cisco AnyConnect.
- **[docs/appstore-sku.md](appstore-sku.md)** — отдельный трек «App Store SKU»
  (только системный VPN, без System Extension); эта страница его не касается.

---

## Раздел 1 — Что получить в Apple Developer

Порядок важен: каждый следующий пункт зависит от предыдущего.

1. **Платное членство Apple Developer Program** ($99/год) на аккаунте с
   Team ID `S3UCF4USYC` (или своим — см. ниже). Бесплатный Apple ID не
   подходит: entitlement Network Extensions и нотаризация доступны только
   платным аккаунтам.

   Свой Team ID, если сборка идёт не под этот проектный аккаунт:
   `security find-identity -v -p codesigning` (в скобках в конце строки
   идентичности), либо developer.apple.com/account → Membership details,
   либо Xcode → Settings → Accounts.

2. **App IDs** в developer.apple.com/account → Identifiers — два, вложенных
   друг в друга (extension — суффикс app):

   | Роль | Bundle ID | Capabilities |
   |---|---|---|
   | Контейнер-приложение | `com.singctl.proxy` | App Groups, Network Extensions |
   | System Extension | `com.singctl.proxy.netext` | App Groups, Network Extensions |

3. **App Group `group.com.singctl.proxy`**, включённый у обоих App ID выше —
   общий контейнер, через который CLI отдаёт расширению `config.json`
   (`internal/netext`).

4. **Сертификаты Developer ID Application** и **Developer ID Installer**
   (не App Store Distribution — дистрибуция вне Store, см. врезку в разделе 2).
   Проверить, что оба уже в связке ключей: `security find-identity -v -p codesigning`.

5. **Запрос Developer ID Network Extension entitlement** — распространение
   системного расширения с подписью Developer ID требует отдельного одобрения
   Apple. Подать заявку нужно сразу, как только есть членство:
   https://developer.apple.com/contact/request/network-extension (выбрать
   **Developer ID distribution**). Рассмотрение — от нескольких дней до
   нескольких недель, поэтому не откладывать на конец. Пока одобрения нет,
   локальные Development-сборки всё равно работают через
   `systemextensionsctl developer on` (см. «Траблшутинг» в LICENSATION.md
   часть B).

6. **Provisioning-профили уровня Developer ID** для обоих App ID, с включённым
   `*-systemextension`-entitlement Network Extensions (на время разработки
   можно временно обойтись Development-профилями и автоподписью в Xcode).

7. **Учётные данные `notarytool`** — сохранить один раз в связку ключей:

   ```sh
   xcrun notarytool store-credentials singctl-notary \
     --apple-id <you@example.com> --team-id S3UCF4USYC --password <app-specific-pwd>
   ```

   Пароль — app-specific (appleid.apple.com → Sign-In and Security →
   App-Specific Passwords), не пароль от аккаунта.

Подробное описание каждого пункта (entitlements-файлы, XML-примеры,
траблшутинг) — [LICENSATION.md, часть B, §0–3](../LICENSATION.md); что
конкретно ещё требует Mac-разработки в самом расширении — в
[RUNBOOK.md](../packaging/macos/netextension/RUNBOOK.md).

---

## Раздел 2 — Сборка лицензионного билда

### Что делает билд «лицензионным»

Два независимых механизма, оба встраиваются на этапе `go build` через
`-ldflags -X`:

- **Проверка токена офлайн.** CLI/GUI проверяют Ed25519-подписанный токен
  встроенным публичным ключом (`LICENSE_PUBKEY` → `internal/license.PublicKeyB64`).
  Без него `license.Enabled()` возвращает `false`, и сборка не умеет проверять
  никакие лицензии вообще (это `make build-unlicensed`, только для локальной
  разработки — CI никогда его не публикует).
- **Куда активироваться и поллить.** `LICENSE_SERVER_URL` встраивается как
  `internal/license.LicenseServerDefault` — базовый URL лицензионного сервера
  (см. [docs/deploy-gitlab.md](deploy-gitlab.md) про разворачивание самого
  сервера). Переопределяется на рантайме без пересборки переменной окружения
  `SINGCTL_LICENSE_SERVER` (`internal/license/pubkey.go`). Если ни то ни
  другое не задано, `license.ServerURL()` возвращает пустую строку — онлайн-
  активация и суточная сверка отключаются, остаётся только офлайн-проверка
  подписи (с предупреждением в лог).

Логика активации (`cmd/singctl/license.go:enforceLicense`,
`internal/license/decide.go:DecideEnforcement`, зеркалируется в GUI —
`gui/bridge/license.go`):

- **Первое подключение обязательно.** Пока токен ни разу не подтверждён
  сервером (`ActivatedOnce == false`), любая сетевая ошибка блокирует запуск
  сообщением «Для первой активации лицензии нужен доступ к серверу». Токен
  сам по себе, скопированный без первого онлайн-подтверждения, не работает.
- **Дальше — суточный поллинг.** Раз в 24 часа (`time.NewTicker(24 * time.Hour)`
  и в CLI-демоне, и в GUI) сервер опрашивается заново.
- **Офлайн-толерантность бессрочная.** После первой успешной активации любая
  сетевая ошибка при последующих проверках трактуется как «оффлайн» и не
  блокирует работу — состояние с диска используется как есть, сколько угодно
  долго. Блокирует только **достижимый** сервер, явно ответивший
  `revoked`/`expired`.
- **Отзыв виден не мгновенно**, а на следующей успешной проверке (следующий
  запуск CLI или ближайший суточный тик) — задержка до ~24 часов, если машина
  в сети; на офлайн-машине отзыв не увидят вовсе, пока она не выйдет в сеть.

### Шаги сборки

1. **Сгенерировать ключевую пару** (один раз на проект/сервер):

   ```sh
   make build-server
   bin/singctl-server keygen
   # LICENSE_PUBKEY=…       — публичный, встраивается в сборку, коммитить можно
   # LICENSE_PRIVATE_KEY=…  — приватный, только на сервер, никогда не коммитить
   ```

2. **Собрать CLI** (реальное sing-box-ядро, CGO):

   ```sh
   make build-macos LICENSE_PUBKEY=<pub> LICENSE_SERVER_URL=https://license.<домен>
   ```

3. **Собрать GUI** (Wails-приложение):

   ```sh
   make gui GUI_TAGS= LICENSE_PUBKEY=<pub> LICENSE_SERVER_URL=https://license.<домен>
   ```

   `GUI_TAGS=` обязателен — без него по умолчанию собирается dev-вариант с
   тегом `unlicensed` (проверка лицензии скомпилирована из GUI и экран
   лицензии показывает «development build»).

4. **Собрать System Extension + контейнер-приложение** (per-app изоляция,
   отдельно от лицензии, macOS-only, нужен Xcode + XcodeGen):

   ```sh
   make build-netext DEVELOPMENT_TEAM=S3UCF4USYC
   ```

5. **Собрать `.pkg`/`.dmg`**:

   ```sh
   make pkg-macos PKG_VERSION=x.y.z CLI_BIN=bin/singctl-darwin-arm64
   ```

   `pkg-macos` (`packaging/macos/build-installers.sh`) сам подписывает
   staged-копию CLI отдельным entitlements-файлом с App Group
   (`packaging/macos/singctl-cli.entitlements`) — иначе CLI не сможет писать
   общий `config.json` для расширения. Подпись GUI-`.app` идёт с
   `packaging/macos/singctl.entitlements` (sandbox выключен, как у
   clash-verge-rev — GUI общается с демоном через unix-сокет).

   Переменные окружения, включающие реальную подпись/нотаризацию (без них
   получаются несигнированные артефакты — годится для dry-run):

   | env | назначение |
   |---|---|
   | `CODESIGN_IDENTITY` | `Developer ID Application: <Name> (S3UCF4USYC)` — подпись `.app` и CLI |
   | `INSTALLER_IDENTITY` | `Developer ID Installer: <Name> (S3UCF4USYC)` — подпись `.pkg` |
   | `NOTARY_PROFILE` | keychain-профиль `notarytool` (альтернатива — тройка `AC_*` ниже) |
   | `AC_APPLE_ID` / `AC_PASSWORD` / `AC_TEAM_ID` | учётные данные нотаризации напрямую, напр. `AC_TEAM_ID=S3UCF4USYC` |

   Скрипт сам нотаризует и стейплит `.pkg` и `.dmg`, если заданы креды
   (`NOTARY_PROFILE` или `AC_*`). Проверка результата:

   ```sh
   spctl -a -vv dist/singctl-x.y.z.pkg      # Gatekeeper согласен
   xcrun stapler validate dist/singctl-x.y.z.pkg
   ```

> **Почему Developer ID, а не App Store.** System Extension нельзя
> распространять через Mac App Store; системный VPN/TUN-режим требует root и
> несовместим с обязательным для Store App Sandbox; голый CLI в Store тоже не
> публикуется. Поэтому путь дистрибуции — подписанный Developer ID `.app`/
> `.pkg` + нотаризация, вне Store. Отдельный трек с системным VPN и App
> Sandbox, который **можно** отдать в App Store, описан в
> [docs/appstore-sku.md](appstore-sku.md) — это не тот путь, что здесь.

### Автоматизация

`.gitlab-ci.yml` → джоба `release:macos` (ручная, `when: manual`, раннер с
тегом `macos`) воспроизводит шаги 2–5 на теге `vX.Y.Z`. Полное описание
переменных CI/CD и порядка регистрации Mac-раннера —
[docs/deploy-gitlab.md, шаг 6](deploy-gitlab.md#шаг-6--релизы).

> **Расхождение на момент написания:** `release:macos` в `.gitlab-ci.yml`
> сейчас передаёт в сборку только `LICENSE_PUBKEY` (`make build-macos
> LICENSE_PUBKEY="$LICENSE_PUBKEY"`, `make gui GUI_TAGS=
> LICENSE_PUBKEY="$LICENSE_PUBKEY"`) — `LICENSE_SERVER_URL` в джобе не
> проброшен (переменная в `Makefile` появилась позже джобы). Пока это не
> починено в `.gitlab-ci.yml`, CI-релиз macOS выходит без встроенного адреса
> сервера: активация/суточная сверка отключены, и после установки нужно
> явно задавать `SINGCTL_LICENSE_SERVER` на машине пользователя, либо
> добавить свою переменную CI/CD `LICENSE_SERVER_URL` и дописать её в
> команды джобы, либо собирать `.pkg`/`.dmg` локально по шагам выше.

---

## Раздел 3 — Бесплатная раздача команде

Приложение остаётся **лицензионным** (гейт включён, сервер настроен), но
раздаётся команде **бесплатно** — вместо продажи каждому коллеге просто
выпускается свой **бессрочный** токен.

### Выпуск бессрочного токена

Семантика поля `days` **различается** между CLI и HTTP-эндпоинтом — важно не
перепутать:

- **CLI `bin/singctl-server issue`** (`cmd/server/admin.go`): флаг
  `--days` (default `365`); в коде — `if *days > 0 { ttl = days*24h }
  else { ttl = 0 (бессрочно) }`. То есть **и `0`, и отрицательное** значение
  дают бессрочную лицензию.
- **HTTP `POST /v1/admin/issue`** (`internal/licensesrv/handlers.go`,
  `ttlFromDays`): `days == 0` → **дефолтный TTL сервера**
  (`LICENSE_DEFAULT_TTL_DAYS`, обычно 365 дней), и только **отрицательное**
  значение → бессрочно. `0` через HTTP — это НЕ бессрочно, в отличие от CLI.

Чтобы не зависеть от этой разницы, **всегда указывай явно отрицательное
значение** (`-1`) для бессрочного токена — так оно бессрочно в обоих путях.

**Локально на сервере** (тот же JSON-store, что использует `serve`):

```sh
LICENSE_PRIVATE_KEY=<priv> LICENSE_DB=/data/licenses.json \
  bin/singctl-server issue --subject коллега@почта.ru --days -1
# печатает токен в stdout, "issued license <id> for …" — в stderr
```

**Через админ-API** (когда сервер уже развёрнут — см.
[docs/deploy-gitlab.md](deploy-gitlab.md)):

```sh
curl -fsS -XPOST https://license.<домен>/v1/admin/issue \
  -H "Authorization: Bearer $LICENSE_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"subject":"коллега@почта.ru","days":-1}'
# → {"id":"…","token":"…"}
```

Поле `subject` обязательно, `features` — необязательный массив строк
(feature-флаги в токене).

### Раздать коллеге

1. Нотаризованный `.dmg` (или `.pkg`) + токен — любым каналом (почта, Slack,
   USB), токен не обязан быть секретным в узком смысле, но не публикуй его
   без нужды (это именной идентификатор лицензии, который можно отозвать).

**Установка у коллеги:**

1. `.dmg` → перетащить `SingctlProxy`/`singctl-gui` в Applications (или
   запустить `.pkg` — он дополнительно ставит CLI в `/usr/local/bin` и
   LaunchDaemon для системного VPN-режима).
2. Первый запуск — Gatekeeper молчит (подпись Developer ID + нотаризация
   пройдены).
3. Если нужна **per-app изоляция** (не системный VPN) — одобрить расширение:
   System Settings → General → Login Items & Extensions → Network Extensions
   (см. [docs/macos.md](macos.md#per-app-на-macos--системное-расширение)).
   Для обычного системного VPN-режима этот шаг не нужен.
4. **Активировать лицензию:**
   - в GUI — вставить токен в разделе лицензии (`ActivateLicense`,
     `gui/frontend/src/features/activate-license`); GUI сразу же обращается к
     серверу для подтверждения активации;
   - в CLI — `singctl --license <токен>` сохраняет и офлайн-проверяет токен
     (без похода на сервер), а онлайн-активация происходит на **следующем**
     обычном запуске `singctl` (в т.ч. если он поднят как LaunchDaemon —
     активация пройдёт при следующем старте демона).

Первая активация **обязательно** требует интернет (см. раздел 2); после неё
приложение работает офлайн сколько угодно, с ежедневной попыткой сверки при
наличии сети. При отзыве блокировка наступит на следующей успешной проверке
(до ~24 часов online, либо при следующем запуске).

### Отзыв

```sh
LICENSE_DB=/data/licenses.json bin/singctl-server revoke --id <license-id>
# или через HTTP:
curl -fsS -XPOST https://license.<домен>/v1/admin/revoke \
  -H "Authorization: Bearer $LICENSE_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"<license-id>"}'
```

Список всех лицензий (кто выпущен, статус) — `GET /v1/admin/licenses` под тем
же bearer-токеном.

---

## Раздел 4 — Чеклист (сводка)

1. **Apple Developer portal** — платное членство под Team ID `S3UCF4USYC` →
   App IDs (`com.singctl.proxy`, `com.singctl.proxy.netext`) → App Group
   `group.com.singctl.proxy` → сертификаты Developer ID (Application +
   Installer) → запрос Network Extension entitlement (подать сразу) →
   provisioning-профили → `notarytool store-credentials`.
2. **`bin/singctl-server keygen`** → сохранить `LICENSE_PUBKEY` (в сборку) и
   `LICENSE_PRIVATE_KEY` (на сервер, не коммитить).
3. **Развернуть лицензионный сервер** — [docs/deploy-gitlab.md](deploy-gitlab.md).
4. **Собрать лицензионный билд** — `make build-macos` / `make gui` (с
   `LICENSE_PUBKEY` + `LICENSE_SERVER_URL`) → `make build-netext` →
   `make pkg-macos` (с `CODESIGN_IDENTITY`/`INSTALLER_IDENTITY`/`NOTARY_PROFILE`).
5. **Нотарификация + staple** — встроено в `pkg-macos`, проверить
   `spctl -a -vv` / `xcrun stapler validate`.
6. **Выпустить токены** — `bin/singctl-server issue --subject <коллега> --days -1`
   (или HTTP `/v1/admin/issue` с `"days":-1`) — по одному на человека,
   бессрочный.
7. **Раздать** — нотаризованный `.dmg`/`.pkg` + личный токен каждому.
8. **Активировать** — GUI (вставить токен) или CLI (`singctl --license
   <токен>` + обычный запуск); первая активация требует сеть, дальше — офлайн
   с суточной сверкой.
