# singctl — статус реализации

Прототип реализован полностью по этапам `PLAN.md §9`. Весь герметичный unit-набор
зелёный: `go test ./...` проходит **без root, без реального sing-box, без Cisco**.

## Что сделано (этапы 0–8)

| Пакет | Назначение | Покрытие |
|---|---|---|
| `internal/vless` | парсер `vless://` ссылок (чистый) | 93% |
| `internal/singbox` | генератор двух конфигов + golden | 97% |
| `internal/core` | seam вокруг sing-box (`Core`/`FakeCore`; реальный — за `-tags singbox`) | 100% |
| `internal/runtime` | `Manager` жизненного цикла + prober + routes + config-builder | 71% |
| `internal/netstate` | пассивный детектор Cisco (ifconfig/route/netstat/ps) + PF_ROUTE-события | 73% |
| `internal/policy` | чистый движок решений (правила a/b/c + fail-closed) | 95% |
| `internal/monitor` | опрос + debounce + событийный источник | 88% |
| `internal/ui` | TUI на Bubble Tea (чистый редьюсер) | 83% |
| `internal/app` | executor: мост monitor↔manager, реализация `ui.Backend` | 77% |
| `internal/platform` | резолв реального пользователя (`SUDO_USER`) | 87% |
| `internal/profile` | персист профиля в home + chown | 63% |
| `internal/arch` | guardrail-тесты (sing-box только в core; нет `/opt/cisco`) | — |

Низкое покрытие (`runtime`/`netstate`/`profile`/`cmd`) приходится на тонкие
darwin-адаптеры (`*_darwin.go`, `OSFS`, `main`-склейка), которые по дизайну
проверяются на интеграции, а не юнит-тестами. Вся **логика решений** покрыта.

## Ключевые гарантии, закреплённые тестами

- **Никогда не стартуем VPN при активном Cisco** — инвариант
  `policy.TestDecide_NeverStartsForwarderWhileCiscoActive` (полный кросс-продукт).
- **Полный yield при появлении Cisco** — `policy` + `monitor` + `app` тесты:
  при Cisco↑ глушим forwarder И proxy (`SuspendForCisco`) и ОСТАЁМСЯ выключенными,
  пока Cisco присутствует — AnyConnect аварийно прерывается («не удалось проверить
  изменения в таблице пересылки»), если таблицу трогает другой процесс во время его
  подключения. Proxy возвращается (`ResumeProxy`) ТОЛЬКО когда Cisco отключился.
  Поэтому авто-возврат «через N секунд» намеренно убран: любой старт sing-box при
  активном Cisco ломает его повторное подключение.
- **Cisco не трогаем** — структурный `arch.TestNoCiscoBinaryReferences`
  (нигде в коде нет `/opt/cisco`) + детектор только читает ОС.
- **Forwarder релеит на тот же порт, что слушает proxy** —
  `singbox.TestForwarderRelayTargetMatchesProxySocksInbound`.
- **Обход Cisco невозможен → proxy идёт через Cisco** (D4, проверено эмпирически
  2026-06-02): `bind_interface` ставится только в VPN-режиме.

## Запуск

```
make test               # герметичный unit-набор (этот результат — зелёный)
make build              # реальный бинарник: go get sing-box, затем -tags singbox + CGO
sudo ./bin/singctl      # запуск (TUN/VPN требует root)
make test-singbox-decode  # проверка, что конфиги принимает реальная схема sing-box 1.12
```

Перед `make build` / `make test-singbox-decode` один раз подтянуть ядро:

```
go get github.com/sagernet/sing-box@v1.12.x
```

## Что осталось проверить на живой машине (НЕ покрыто юнит-тестами)

Это HARD-пункты из `PLAN.md §8` — требуют root + реального sing-box + живого
Cisco. Их нужно прогнать перед боевым использованием:

1. **Loop-safety**: при поднятом TUN трафик proxy к `193.188.22.147` уходит через
   `en0` (bind_interface), а не зацикливается — проверить пакетным счётчиком;
   отдельно UDP/DNS.
2. **acsockext passthrough при выключенном Cisco**: что bind к en0 escape-ит наш
   TUN, когда Cisco отключён (при активном — заблокирован, это уже доказано).
3. **Listener continuity**: реальный размер разрыва `1080/2080` при RefreshProxy.
4. **Два бокса** сосуществуют в одном процессе без паники на дубль-регистрации.
5. **Graceful Close / SIGKILL + CleanupOrphans**: восстановление маршрутов и снос
   только нашего `198.18.0.x` utun (никогда Cisco `172.18.x`).
6. **Событийная детекция (PF_ROUTE)**: миллисекундная реакция fail-closed.

## Уже подтверждено реальной сборкой (2026-06-02, sing-box v1.13.12)

- ✅ `make test-singbox-decode` — оба конфига (proxy + forwarder) **приняты схемой
  реального sing-box 1.13.12** без расхождений полей.
- ✅ `make build` (`-tags "singbox with_utls"` + CGO) — единый бинарник со встроенным
  ядром и TUN собирается (≈27 MB). API-рецепт встраивания (`box.Context` 6-арг,
  `box.Options{Context, Options}`, `json.UnmarshalExtendedContext`) корректен.
- ✅ `box.New` для proxy И forwarder конфигов проходит — REALITY/uTLS и gRPC/TUN
  инициализируются.
- **Feature-теги sing-box:** `with_utls` ОБЯЗАТЕЛЕН (REALITY без него падает с
  «uTLS … required by reality is not included»). `with_gvisor` намеренно НЕ
  включён: используем `stack: system`, а сам `with_gvisor` не собирается с
  запиненной версией `sing-tun`. Всё это уже зашито в Makefile (`SINGBOX_TAGS`).
- Работает и на 1.12.x, и на 1.13.x (текущий `go.mod` — 1.13.12).

Остаётся только живая проверка под root (пункты §8 выше): loop-safety, поведение
`acsockext` при выключенном Cisco, размер разрыва listener'ов, очистка orphan.
