# singctl — сборка и запуск

TUI-утилита: поднимает VLESS-прокси на встроенном sing-box и переключает системный
VPN-режим (TUN), пассивно сосуществуя с Cisco Secure Client. Подробности — в `PLAN.md`.

## Требования

- **Go ≥ 1.23** (разработка велась на go1.26.3 darwin/arm64).
- **Xcode Command Line Tools** (`xcode-select --install`) — нужен `clang` для CGO
  (sing-box TUN на darwin собирается с `CGO_ENABLED=1`).
- Целевая платформа — **macOS arm64** (Apple Silicon). Решение D8.

## Команды

```
make test               # герметичный unit-набор: без root, без реального sing-box, без Cisco
make build              # сборка бинарника в ./bin/singctl (CGO_ENABLED=1)
make run                # сборка + запуск под sudo (VPN/TUN требует root)
make test-integration   # интеграционные тесты под root на живой машине (build-tag integration)
make tidy               # go mod tidy
make lint               # go vet
make install-man        # установка man-страницы (`man singctl`), может попросить sudo
```

## Кросс-платформенная сборка

```
make build-macos        # bin/singctl-darwin-{arm64,amd64}   (CGO_ENABLED=1, собирается на Mac)
make build-linux        # bin/singctl-linux-{amd64,arm64}    (Ubuntu и др.; чистый Go, CGO=0)
make build-windows      # bin/singctl-windows-{amd64,arm64}.exe (Windows 10/11; чистый Go, CGO=0)
make build-all          # все шесть бинарников
```

Linux/Windows-сборки кросс-компилируются с любого хоста (CGO не нужен:
sing-tun использует netlink на Linux и wintun на Windows). macOS-сборка требует
CGO и потому собирается на Mac (обе архитектуры — clang кросс-ассемблирует).

Платформенные оговорки:

- **Windows 10/11:** запускать **от администратора**; для VPN (TUN) рядом с
  бинарником нужна `wintun.dll` (https://www.wintun.net). Проверка euid
  пропускается — права проверит сама ОС при создании TUN.
- **Linux (Ubuntu):** запуск под `sudo`, TUN/маршруты — через netlink.
- Пассивная детекция Cisco Secure Client заточена под macOS (парсеры
  `ifconfig`/`netstat`/`ps`); на Linux/Windows она деградирует мягко
  (Cisco просто не обнаруживается), kernel-события маршрутов заменяет
  2-секундный опрос. Очистка orphan-utun — тоже только macOS.

## CLI-флаги

```
sudo singctl [flags]

  -k, --key <vless://...>  vless-ключ (ссылка) для загрузки
      --headless           без TUI: поднять режим и работать до ctrl+c
  -l, --logs               логи sing-box в stdout (headless) / открыть логи при старте (TUI)
  -p, --proxy              включить режим PROXY сразу при старте
      --vpn                включить режим VPN сразу при старте
      --port <n>           локальный SOCKS-порт (HTTP слушает на n+1; по умолчанию 1080/2080)
      --env-file <path>    загрузить переменные окружения из файла (по умолчанию ./.env)
      --no-save            не сохранять ключ в ~/.config/singctl
      --man                вывести man-страницу (roff) и выйти
  -v, --version            версия
  -h, --help               справка
```

Приоритет источников ключа/порта: **флаг → переменные окружения → сохранённый профиль**.
Переменные `SINGCTL_KEY` и `SINGCTL_PORT` читаются из окружения и из `.env`
(godotenv; уже установленные переменные `.env` не перекрывает).

Примеры:

```
sudo singctl --headless --proxy --port 7890 -k 'vless://...'
echo 'SINGCTL_KEY=vless://...' > .env && sudo singctl --headless --vpn --logs
```

## Важно

- Бинарник запускается **под sudo** целиком (решение §0/D2): создание TUN и `auto_route`
  требуют привилегий. Профили сохраняются в домашний каталог реального пользователя
  (резолв через `SUDO_USER`) и `chown`-ятся обратно.
- **Cisco не трогаем.** Состояние Cisco определяется только пассивным наблюдением за ОС;
  бинарники Cisco (`/opt/cisco/...`) не вызываются никогда — это проверяется тестом
  `internal/arch.TestNoCiscoBinaryReferences`.
- Версия sing-box — **v1.12.x или v1.13.x** (проверено на 1.13.12).
- **Feature-теги sing-box:** боевая сборка использует `-tags "singbox with_utls"`
  (`with_utls` обязателен для REALITY). `with_gvisor` НЕ используем — берём
  `stack: system`, а сам он не собирается с запиненным `sing-tun`. Теги уже
  заданы в Makefile (`make build`).
