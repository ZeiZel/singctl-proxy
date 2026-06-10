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
