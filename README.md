# singctl

`singctl` — VLESS-прокси/VPN-клиент на встроенном ядре **sing-box** (v1.13.12).
Поднимает локальный SOCKS/HTTP-прокси и умеет включать системный VPN-режим (TUN),
пассивно сосуществуя с Cisco Secure Client (AnyConnect). Главный сценарий —
проксировать трафик выбранных приложений через корпоративный VPN, чтобы получить
им исходящий доступ.

Проект разворачивается из CLI/TUI в готовый к продаже продукт: к утилите
добавляются фоновый сервис-демон (старт при загрузке), десктоп-GUI в стиле
clash-verge-rev и сервер лицензий.

## Компоненты

| Компонент | Где | Что делает |
| --- | --- | --- |
| **CLI / TUI** | `cmd/singctl` | терминальный интерфейс (Bubble Tea, рус.), флаги, man-страница |
| **Демон** | `singctl --daemon --headless` | фоновый рутовый сервис (LaunchDaemon/systemd), держит прокси и отдаёт control-сокет |
| **Десктоп-GUI** | `gui/` | Wails-приложение (Go + React, англ.), управляет демоном — см. [`gui/README.md`](gui/README.md) |
| **Сервер лицензий** | `cmd/server`, `internal/licensesrv` | выпуск/отзыв/проверка Ed25519-лицензий — см. [`LICENSATION.md`](LICENSATION.md) |

GUI **непривилегированный**: он не линкует sing-box и не требует root — все
привилегированные операции (TUN, маршруты, cgroup/nftables) живут в демоне, а GUI
управляет им через control-сокет + Clash API.

## Архитектура

Гексагональная: чистое ядро решений/конфигурации, окружённое инъектируемыми
портами ввода-вывода; единственная зависимость от sing-box изолирована за
build-тегом.

- **`internal/singbox`** — генерация JSON-конфига sing-box (чистая, без импорта
  sing-box; контракт проверяется golden-файлами).
- **`internal/core`** — абстракция над ядром: реальное (`real.go`,
  `//go:build singbox`) и заглушка/фейк для дефолтной сборки и тестов.
- **`internal/runtime`** — `Manager`: жизненный цикл двух инстансов sing-box
  (постоянный PROXY + on-demand TUN-форвардер), машина состояний.
- **`internal/policy` / `netstate` / `monitor`** — пассивное наблюдение за сетью и
  Cisco + чистый движок сосуществования (observe-only, никогда не трогаем маршруты
  AnyConnect).
- **`internal/control`** — анонс инстанса (`instance.json`) + Unix control-сокет
  (`MODE/SETTINGS/KEYS/PROC-*/TRAFFIC/CONSOLE-POLL`).
- **`internal/clashapi`** — read-only клиент Clash API sing-box (живые соединения,
  латентность, трафик).
- **`internal/app`** — `Executor`: композиционный корень, реализует `ui.Backend`.
- **`internal/ui` / `remote`** — TUI (чистый редьюсер) и backend для управления
  уже запущенным демоном.

Подробности — в [`PLAN.md`](PLAN.md); статус по пакетам — в [`STATUS.md`](STATUS.md).

### Два инстанса sing-box

- **PROXY** (постоянный): SOCKS `127.0.0.1:1080` + HTTP `127.0.0.1:2080`.
- **TUN-форвардер** (только в VPN-режиме): владеет маршрутом по умолчанию через
  `auto_route`, релеит всё в PROXY, перехватывает DNS. Подсеть `198.18.0.0/30`.

## Структура репозитория

```
cmd/singctl     CLI/TUI + точка сборки шиппинг-бинарника
cmd/server      сервер лицензий
internal/       ядро (см. «Архитектура»); только core/real.go импортирует sing-box
gui/            десктоп-GUI (Wails: Go + React) — отдельный модуль singctl/gui
packaging/      macOS (LaunchDaemon, NetworkExtension) + Linux (systemd unit)
scripts/        install-macos.sh / install-linux.sh
deploy/         сервер лицензий: Dockerfile, Helm-чарт, Ansible
docs/           дополнительная документация
```

## Сборка и тесты

Дефолтная сборка/тесты **не импортируют sing-box** (линкуется заглушка) — набор
быстрый и без CGO. Реальное ядро подключается только под `-tags singbox`.

```sh
go build ./...                      # дефолтная сборка (stub-ядро)
go test ./...                       # герметичный unit + golden набор (FakeCore)
go build -tags singbox ./...        # шиппинг-сборка с реальным ядром sing-box
go test ./internal/singbox -update  # перегенерировать golden-конфиги (ревьюить дифф!)
```

Основные `make`-цели (полный список — `README-build.md`):

```sh
make build              # версионированный бинарник в ./bin/singctl
make build-all          # кросс-сборка macOS/Linux/Windows
make build-server       # сервер лицензий
make install            # установка CLI + демона (LaunchDaemon на macOS, systemd на Linux)
make gui                # сборка десктоп-GUI (wails build -tags webkit2_41)
make gui-dev            # GUI в режиме разработки
make gui-test           # тесты GUI (Go bridge + frontend vitest)
```

`singctl` для VPN/TUN запускается **под root** (управление TUN и маршрутами);
демон ставится как системный сервис и стартует при загрузке. Под sudo реальный
пользователь резолвится из `SUDO_USER` для владения файлами в `~/.config/singctl`.

Подробные платформенные оговорки (Windows/wintun, кросс-компиляция, man-страница) —
в [`README-build.md`](README-build.md).

## Десктоп-GUI

Сборка и разработка GUI описаны в [`gui/README.md`](gui/README.md): требования
(Wails CLI, GTK/WebKit на Linux, тег `webkit2_41`), `make gui`/`gui-dev`/`gui-test`,
структура (Feature-Sliced Design + Tailwind + Zustand) и соглашения.

## Лицензирование и поставка

Сервер лицензий, выпуск токенов, подпись/нотаризация под macOS и дистрибуция
описаны в [`LICENSATION.md`](LICENSATION.md). Деплой сервера (Docker/Helm/Ansible) —
в `deploy/`.

## Разработка

- **Трекинг задач — beads (`bd`)**, не TODO-списки. `bd prime` — контекст и
  команды, `bd ready` — доступная работа.
- Чистые пакеты (`vless`, `singbox`, `policy`) тестируются напрямую; всё с
  вводом-выводом — за интерфейсом с фейком.
- Golden-тесты для всего JSON sing-box (не править руками — перегенерировать).
- Строки TUI/логов — на русском; интерфейс GUI — на английском.

Инструкции для ИИ-агентов и инварианты, которые нельзя ломать, — в
[`CLAUDE.md`](CLAUDE.md) / [`AGENTS.md`](AGENTS.md).
