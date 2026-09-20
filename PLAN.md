# План реализации singctl

> Документ для ревью ПЕРЕД написанием кода. Технические идентификаторы (Go-типы и пакеты, поля sing-box вроде `bind_interface`/`auto_route`/`hijack-dns`, имена команд) оставлены в оригинальной форме.

---

## 0. Решения по итогам ревью (финал, приоритетны над разделами ниже)

Зафиксировано после обсуждения с пользователем. Где эти решения противоречат тексту ниже — приоритет за этим разделом.

- **D1. Версия sing-box — пин v1.12.x.** Под существующую 1.12-схему конфигов (типизированный DNS `type`, `route.action`). Закрывает открытый вопрос 1.
- **D2. Только локально, без публикации.** `go.mod` → `module singctl` (без `github.com/...`); внутренние импорты `singctl/internal/...`. Артефакт наружу не уходит. Закрывает вопрос 2 (module path).
- **D3. `bind_interface` ставится ТОЛЬКО в VPN-режиме** (где Cisco по политике выключен) — чтобы вырваться из нашего же TUN. В proxy-only режиме по умолчанию bind НЕТ. Следствие: вход/выход из VPN пересоздаёт PROXY-бокс (добавить/снять bind) → принятый sub-second разрыв слушателей. Это снимает противоречие «set once» из R1: `default_interface` НЕ статичен между режимами. Закрывает вопросы 3 и 4 (часть).
- **D4. Обход Cisco в proxy-режиме НЕВОЗМОЖЕН — проверено эмпирически (2026-06-02). Опция `--proxy-bind` исключена.** Прямой тест на живой машине (Cisco активен): TCP к VLESS-серверу `193.188.22.147:443` с привязкой к en0 через `IP_BOUND_IF` (= `bind_interface`) → `Connection timed out`; тот же запрос через `utun4` (Cisco) → `Connected`, и en0-bind к Cisco-исключённому адресу → `Connected`. То есть механизм привязки исправен, но Cisco удерживает туннелируемые адреса на уровне **сокет-фильтра** (system extension `com.cisco.anyconnect.macos.acsockext`) ВЫШЕ таблицы маршрутов: scoped-route на en0 существует, но пакеты не проходят. Вывод: **в proxy-only режиме трафик идёт через Cisco** (как и сейчас), `bind_interface` в proxy-режиме НЕ ставится. Окончательно закрывает развилку R1/вопрос 4.
- **D5. Детекция Cisco — событийная (основная) + поллинг (страховочный).** Основной триггер — события `PF_ROUTE`-сокета и/или `SCDynamicStore` (появление/исчезновение чужого `utun`/маршрута за миллисекунды). Поллинг ~2с с debounce остаётся fallback на случай пропуска события. Обязательно для требования «ни единым пакетом». Закрывает вопрос 7.
- **D6. Fail-closed teardown при появлении Cisco.** Новый `Action` = `ActFailClosed`: в момент детекта чужого туннеля СНАЧАЛА мгновенно прекращаем форвардинг (пакеты в нашем TUN дропаются, НЕ уходят в VLESS), и только ПОТОМ сносим TUN (`ActStopForwarder`). Так ни один прикладной пакет не туннелируется через наш сервер в переходном окне.
- **D7. TUN-подсеть — `198.18.0.0/30`** (вместо `172.18.0.1/30`), чтобы исключить коллизию с пулом Cisco (`172.18.x/22` подтверждён на машине). Закрывает вопрос 6.
- **D8. Сборка — только arm64** (Apple Silicon, локально). Universal-бинарь не нужен. Закрывает вопрос 10.

### Что это даёт по требованию «Cisco не должен идти поверх моего туннеля»

1. **Control-plane Cisco структурно не идёт через нас.** Cisco (как любой VPN-клиент) привязывает соединение к своему headend к физическому интерфейсу (host-route / `IP_BOUND_IF`), иначе его туннель зациклился бы. Поэтому handshake/keepalive Cisco не попадают в наш VLESS-туннель даже до нашей реакции.
2. **Прикладной трафик в микроокне гонки — дропается, а не туннелируется** (D6 fail-closed), причём окно сведено к миллисекундам событийной детекцией (D5).
3. **Честная формулировка гарантии:** это «практически ноль конфликта», а НЕ математический ноль на уровне ядра. Жёсткий pf kill-switch сознательно НЕ выбран (инвазивность). Если в тестах микроокно окажется проблемой — pf kill-switch остаётся возможным апгрейдом.
4. **Сосуществование Cisco + наш VPN физически невозможно** (D4: обход Cisco не проходит, а два full-tunnel дерутся за дефолт). Поэтому политика «VPN запрещён при активном Cisco; при появлении Cisco — мгновенный fail-closed + снос нашего TUN» — это не перестраховка, а единственный корректный режим.

---

## 1. Цель и область

`singctl` — это локальная терминальная утилита (TUI на Bubble Tea) для macOS, которая поднимает прокси на базе VLESS-Reality (sing-box, встроенный как библиотека) и опционально включает «VPN-режим» — системный TUN-форвардер, заворачивающий весь трафик в этот прокси. Утилита умеет:

- разобрать `vless://` share-ссылку в нейтральную модель `ServerProfile`;
- из одного профиля детерминированно сгенерировать ДВА sing-box конфига: постоянный proxy (socks `127.0.0.1:1080` + http `127.0.0.1:2080`) и отдельный TUN-форвардер;
- держать постоянный proxy всегда поднятым и переключать VPN-режим, не роняя слушающие сокеты `1080/2080` (при переключении VPN; ограничения по re-dial см. раздел 10);
- ПАССИВНО наблюдать за состоянием стороннего корпоративного VPN (Cisco Secure Client) и применять политику безопасного сосуществования.

### Явные не-цели (non-goals)

- **Только Cisco отслеживается, и только пассивно.** Никаких бинарников из `/opt/cisco` не вызывается, состояние Cisco НЕ читается из его API/CLI — только косвенно по таблице маршрутов, флагам интерфейсов и read-only списку процессов. Никаких мутаций сети Cisco.
- **Не управляем чужими VPN.** Мы не подключаем/отключаем Cisco, не трогаем его маршруты/utun.
- **Не настоящий «hot reload» sing-box.** В библиотеке sing-box нет in-place перезагрузки полного конфига; «reload» = атомарный stop+recreate (см. раздел 10, риск listener continuity).
- **Не кросс-платформенность.** Только macOS (Apple Silicon arm64 в первую очередь). Опциональный universal-бинарь — открытый вопрос.
- **Не полностью статический бинарь.** На macOS нельзя статически слинковать libSystem; цель — единый self-contained бинарь со встроенным sing-box, без внешней загрузки.
- **Не криптовалидация Reality-ключей и не проверка доступности сервера** на этапе парсинга — только структурная валидация ссылки.

---

## 2. Принятые решения

Зафиксированы четыре архитектурных решения:

1. **Go + sing-box как библиотека.** sing-box встраивается как Go-зависимость (`github.com/sagernet/sing-box`), а не вызывается как внешний бинарник. Это даёт единый аудируемый артефакт и программный контроль жизненного цикла через `box.New`/`(*box.Box).Start`/`(*box.Box).Close`.
2. **Запуск под sudo.** Бинарь требует `euid==0` (preflight в `main`), потому что создание TUN и `auto_route` требуют привилегий. Профили и конфиги сохраняются в домашний каталог реального пользователя (резолвится через `SUDO_USER`) и `chown`-ятся обратно на него, чтобы не оставлять root-owned мусор.
3. **Пассивное наблюдение за Cisco.** Состояние Cisco определяется исключительно read-only средствами: enumerate интерфейсов, чтение таблицы маршрутов, read-only список процессов. Структурно отсутствует любой seam, способный вызвать бинарник Cisco.
4. **Постоянный proxy + отдельный TUN-форвардер.** Два независимых инстанса sing-box в одном процессе: постоянный proxy (всегда поднят, слушатели `1080/2080` не пересоздаются при переключении VPN) и отдельный on-demand TUN-форвардер, который только пробрасывает системный трафик в `127.0.0.1:1080`.

---

## 3. Архитектура

### ASCII-диаграмма: два инстанса sing-box и поток трафика

```
                                 ┌─────────────────────────────────────────────┐
                                 │  процесс singctl (один, под sudo/euid 0)      │
                                 │                                               │
   приложения (curl, браузер     │   ┌───────────────────────────────────────┐ │
   с SOCKS/HTTP-прокси)  ───────────▶│  PROXY core (постоянный, box #1)        │ │
        → 127.0.0.1:1080 (socks) │   │  inbounds: socks 1080 + http 2080       │ │
        → 127.0.0.1:2080 (http)  │   │  outbounds:                             │ │
                                 │   │    - vless (Reality/gRPC)  bind=en0 ────────┐
                                 │   │    - direct (ru/private)   bind=en0 ────────┤
                                 │   │  route.default_interface = en0          │ │ │
                                 │   │  DNS: https(detour=proxy)+local         │ │ │
                                 │   └───────────────────────────────────────┘ │ │
                                 │                                              │ │
   весь системный трафик         │   ┌───────────────────────────────────────┐│ │
   (когда VPN включён) ──────────────▶│  TUN-FORWARDER core (on-demand, box #2)││ │
        через TUN utunN          │   │  inbound: tun (auto_route, strict_route││ │
        (default route)          │   │           stack=system, 198.18.0.x/30) ││ │
                                 │   │  rule: hijack-dns (scope tun-in)        ││ │
                                 │   │  outbound: socks → 127.0.0.1:1080 ──────┘│ │  loopback,
                                 │   │  route.auto_detect_interface = true     │ │  НЕ перехват.
                                 │   └───────────────────────────────────────┘ │  auto_route
                                 └──────────────────────────────────────────────┘ │
                                                                                   │
                                              egress в обход TUN (IP_BOUND_IF=en0)  │
   физический интерфейс en0  ◀──────────────────────────────────────────────────┘
   (gw 192.168.1.1) → VLESS-сервер 193.188.22.147:443 и direct ru-трафик
```

Поток: приложение → socks/http `1080/2080` PROXY-инстанса. В VPN-режиме весь системный трафик перехватывается TUN-форвардером (его `auto_route` ставит default route на utunN), форвардер заворачивает всё в `socks → 127.0.0.1:1080`, то есть в тот же PROXY-инстанс. PROXY-инстанс уже маршрутизирует: VLESS-проксируемый трафик и `direct` (ru/private).

### Избегание петли через bind_interface — с учётом вердикта верификации

**Механизм.** TUN-форвардер своим `auto_route` ставит default route на свой utun. Без защиты собственный egress PROXY-инстанса (TCP к VLESS-серверу `193.188.22.147:443` и `direct`-соединения к ru/private IP) был бы перехвачен этим TUN и зациклен обратно в `127.0.0.1:1080` → дедлок/CPU-spin. Структурная защита: на PROXY-инстансе выставляется `route.default_interface = <физический интерфейс>` И `bind_interface = <физ. интерфейс>` на КАЖДОМ outbound. На macOS sing-box компилирует это в `IP_BOUND_IF`, и такие сокеты обходят таблицу маршрутов (и TUN). Соединения самого socks-форвардера к `127.0.0.1:1080` — loopback, и `auto_route` их НЕ перехватывает (loopback исключён). Привязка живёт на сокетах PROXY-инстанса и не зависит от существования TUN, поэтому переключение VPN НЕ требует рестарта PROXY.

**Вердикт верификатора по claim «loop»: `uncertain` (НЕ полностью доказан).** Конъюнкт «не требует рестарта PROXY при toggle VPN» узко подтверждён (привязка — socket-local). Но конъюнкт «ПОЛНОСТЬЮ предотвращает петлю для ВСЕГО proxy-originated трафика (vless И direct)» НЕ доказан и противоречит открытым вопросам самого дизайна:

1. **Привязка `direct` outbound не подтверждена.** Неизвестно, честно ли sing-box применяет `bind_interface` к outbound типа `direct` на darwin. Если нет — ru/private `direct`-трафик может быть перехвачен `auto_route` и зациклен.
2. **DNS/UDP не подтверждены.** Сервер `type: local` и `default_domain_resolver: local` резолвят через OS/route-default dialer, а не через outbound; если этот dialer не наследует bind — UDP-запросы уйдут в TUN-default и зациклятся.
3. **Внутреннее противоречие «set once / no restart».** Boundary «PROXY-vs-Cisco coexistence» хочет, чтобы PROXY в proxy-only режиме при поднятом Cisco шёл через `utun4` Cisco, а статически зашитый `default_interface=en0` это ломает. Это надо решить (см. раздел 10).

**Конкретные митигации/фоллбэки (обязательны к реализации, раз claim не `holds`):**

- (a) Привязать ВСЕ outbounds: и `vless`, и `direct`, через `bind_interface=physIface`, плюс `route.default_interface=physIface`. Интеграционный тест должен проверить пакетным счётчиком, что сокет к `193.188.22.147` уходит через `en0`, а не через utun.
- (b) Для DNS убедиться, что UDP-сокеты получают `IP_BOUND_IF`; если нет — дать proxy-DNS явный bound dialer ИЛИ гнать весь DNS через `detour=proxy`, чтобы не осталось ни одного непривязанного UDP-сокета.
- (c) **Belt-and-suspenders (фоллбэк):** на ФОРВАРДЕРЕ добавить route-rule, отправляющий IP VLESS-сервера (`193.188.22.147/32`), физический gateway и loopback в `direct`/bypass — тогда даже непривязанный сокет PROXY не зациклится.
- (d) В генераторе НЕ выставлять `auto_detect_interface=true` на PROXY одновременно с `default_interface` — они конфликтуют.
- (e) Запинить версию sing-box и добавить интеграционный тест, проверяющий, что vless И direct И DNS-сокеты несут привязку, ПРЕЖДЕ чем полагаться на универсальный claim.

Вывод: структурная половина (генерация bind-полей) реализуется как описано, но «полнота» защиты — это HARD-пункт интеграционного чек-листа (раздел 8), а не доказанное свойство. Фоллбэк (c) делает систему loop-safe независимо от покрытия привязки.

---

## 4. Структура проекта

**Go module path:** `github.com/mikhailvlasov/singctl` (подтвердить — открытый вопрос). `go 1.23` (требование sing-box 1.12).

```
prototype/
├── go.mod                         # module path, go 1.23, пины sing-box/bubbletea/bubbles/lipgloss
├── go.sum                         # checksum lock (go mod tidy), коммитится
├── Makefile                       # build/build-arm64/universal/tidy/test/run/lint/clean
├── README-build.md                # bootstrap Go, Xcode CLT, запуск под sudo, пин версии
├── cmd/
│   └── singctl/
│       └── main.go                # entry point: флаги, root ctx + signal cancel, sudo preflight,
│                                  #   сборка реальных boundaries, runtime.Manager, запуск Bubble Tea
├── internal/
│   ├── core/                      # встраивание sing-box (ЕДИНСТВЕННОЕ место импорта box.*)
│   │   ├── core.go                # Core interface (Start/Close/Reload), Factory, Options alias
│   │   ├── boxcore.go             # RealCore над *box.Box (build tag по умолчанию)
│   │   ├── include.go             # регистрация протоколов (include.*Registry), один раз
│   │   ├── fakecore.go            # FakeCore для тестов менеджера (без OS/сети)
│   │   └── options.go             # DecodeOptions (context-aware JSON-декодер sing-box)
│   ├── runtime/                   # Manager жизненного цикла двух инстансов
│   │   ├── manager.go             # StartProxy/StartForwarder/StopForwarder/RefreshProxy/Cleanup
│   │   ├── core.go                # Core interface + CoreFactory (runtime-local seam)
│   │   ├── configbuilder.go       # BuildProxyOptions/BuildForwarderOptions (или делегат singbox)
│   │   ├── interfaceprober.go     # InterfaceProber: PhysicalDefault/ListTunnels (darwin)
│   │   ├── routecontroller.go     # RouteController: Snapshot/OrphanTunRoutes/DeleteRoute/FlushTun
│   │   ├── types.go               # SingboxOptions, Iface, Route, TunConfig, ManagerState, OrphanReport
│   │   ├── clock.go               # Clock/Ticker + real/fake
│   │   ├── exec.go                # ExecRunner + osExecRunner/fakeExecRunner
│   │   └── fakes_test.go          # общие тест-дубли пакета
│   ├── platform/                  # агрегированные OS-boundary интерфейсы
│   │   ├── platform.go            # набор Exec/FS/Net/Routes/Procs/Clock для main и тестов
│   │   ├── user.go                # RealUser: резолв SUDO_USER → uid/gid/home
│   │   ├── clock.go               # Clock interface + real/fake
│   │   └── exec.go                # Exec interface (read-only команды; НИКОГДА /opt/cisco)
│   ├── vless/                     # ЧИСТЫЙ парсер ссылки (без I/O)
│   │   ├── profile.go             # ServerProfile + enum'ы (SecurityType/TransportType)
│   │   ├── parse.go               # ParseLink(raw) (ServerProfile, error)
│   │   ├── errors.go              # типизированные sentinel-ошибки
│   │   └── parse_test.go          # table-driven тесты
│   ├── singbox/                   # ЧИСТЫЙ генератор конфигов (без I/O)
│   │   ├── schema.go              # Go-структуры с json-тегами под точную форму sing-box
│   │   ├── generate.go            # GenerateProxyConfig/GenerateForwarderConfig + MarshalIndented
│   │   ├── generate_test.go       # table + golden
│   │   └── testdata/
│   │       ├── proxy.golden.json
│   │       └── forwarder.golden.json
│   ├── netstate/                  # ПАССИВНЫЙ детектор Cisco/состояния сети
│   │   ├── netstate.go            # NetState, TunnelIface, Detector, Observe()
│   │   ├── sources.go             # CommandRunner/InterfaceLister/Clock + value-типы
│   │   ├── osreal.go              # реальные реализации (абсолютные пути, //go:build darwin)
│   │   ├── parse_route.go         # ParseRouteGetDefault/ParseNetstatDefaults (чистые)
│   │   ├── parse_ifconfig.go      # ParseIfconfig (NOARP-бит обязателен)
│   │   ├── classify.go            # classifyTunnels (ours vs foreign)
│   │   ├── cisco.go               # detectCiscoProcess (корроборация)
│   │   ├── netstate_test.go
│   │   └── testdata/              # captured fixtures (см. раздел 8)
│   ├── policy/                    # ЧИСТЫЙ движок решений
│   │   ├── policy.go              # Mode/CiscoState/UserIntent/Action + Decide()
│   │   └── policy_test.go         # полный кросс-продукт переходов
│   ├── monitor/                   # цикл опроса + debounce
│   │   ├── detector.go            # Detector interface + NetState (мост к netstate)
│   │   ├── monitor.go             # Monitor.Run(ctx): poll→debounce→Decide→Event
│   │   ├── debounce.go            # Debouncer (порог N подряд)
│   │   ├── clock.go               # Clock/Ticker
│   │   ├── monitor_test.go
│   │   └── debounce_test.go
│   ├── ui/                        # TUI (Bubble Tea), чистый reducer
│   │   ├── model.go, update.go, view.go, messages.go, commands.go,
│   │   ├── styles.go, ports.go, doc.go
│   │   ├── model_test.go
│   │   └── teatest_test.go
│   ├── profile/
│   │   └── store.go               # персист профиля под home реального пользователя + chown
│   ├── types/                     # (рекомендуется) общий NetState/CoreStatus/Verdict
│   │   └── types.go               # чтобы избежать import-циклов и расхождения контрактов
│   └── testutil/                  # кросс-режущая тест-инфраструктура
│       ├── fakes.go               # все in-memory fakes
│       ├── scenarios.go           # WorldState + CiscoConnected/Disconnected/NetworkChanged
│       ├── golden.go              # AssertGoldenJSON (с -update)
│       ├── doc.go                 # гарантия: без root/тоннеля/Cisco
│       └── testdata/fixtures/     # сырые OS-output фикстуры
```

**Роль ключевых пакетов:** `core` — единственная точка импорта sing-box и seam `Core`; `runtime` — оркестратор жизненного цикла; `vless`+`singbox` — чистое ядро парсинга и генерации; `netstate`+`monitor`+`policy` — пассивное наблюдение и решения; `ui` — презентация; `platform`/`testutil` — OS-границы и фейки.

---

## 5. Подсистемы

### 5.1. core (встраивание sing-box)

- **Назначение:** изолировать sing-box за интерфейсом `Core`; единственное место импорта `box.*`.
- **Ключевые типы/интерфейсы:** `Core interface { Start(ctx) error; Close() error; Reload(ctx, opts option.Options) error }`; `Factory func(ctx, opts) (Core, error)`; `BoxCore` (над `*box.Box`); `FakeCore`; `DecodeOptions(ctx, data) (option.Options, error)`.
- **Внешние зависимости:** `github.com/sagernet/sing-box` (box, include, option), `sing-tun` (транзитивно), CGO для darwin TUN.
- **Тестовые швы:** `Core`/`Factory` (FakeCore); `DecodeOptions` тестируется отдельно (импортирует sing-box → integration-тег).
- **Корректный рецепт встраивания (исправлен по вердикту «embedding»):** `box.Options` НЕ имеет полей-реестров и встраивает `option.Options` (embedded, не именованное поле `Options`). Правильно:
  ```
  ctx := box.Context(context.Background(),
      include.InboundRegistry(), include.OutboundRegistry(),
      include.EndpointRegistry(), include.DNSTransportRegistry(),
      include.ServiceRegistry())               // 6 аргументов, ServiceRegistry обязателен
  b, err := box.New(box.Options{Context: ctx, Options: opts})  // Options — embedded option.Options
  ```
  Реестры регистрируются ОДИН раз (package-level) и шарятся между обоими боксами read-only — дубль-регистрации не возникает.

### 5.2. runtime.Manager (жизненный цикл)

- **Назначение:** владеть двумя `Core` (`proxyCore` постоянный, `tunCore` on-demand), детектить физ. интерфейс, чистить orphan TUN, делать RefreshProxy.
- **Ключевые типы:** `Manager` (поля `core CoreFactory`, `routes RouteController`, `prober InterfaceProber`, `clock`, `build ConfigBuilder`, `proxy/fwd Core`, `boundIface string`, `state ManagerState`); методы `StartProxy/StartForwarder/StopForwarder/RefreshProxy/CleanupOrphans/State`.
- **Внешние зависимости:** только через инъекцию (`CoreFactory`, `RouteController`, `InterfaceProber`, `Clock`, `ExecRunner`); реальные адаптеры — за build-тегами.
- **Тестовые швы:** `CoreFactory`→FakeCore; `RouteController`/`InterfaceProber`/`Clock`/`ExecRunner` — фейки; ordering-recorder для проверки порядка вызовов.

### 5.3. platform

- **Назначение:** агрегировать OS-границы (`Exec`, `FS`, `Net`, `Routes`, `Procs`, `Clock`) и резолв пользователя.
- **Ключевые типы:** `RealUser{Uid,Gid,HomeDir,Username}`; `Clock`; `Exec interface { Run(ctx,name,args...) ([]byte,error) }` (read-only).
- **Тестовые швы:** `RealUser` через инъекцию `env func(string)string` + `UserLookup`; `FS`/`Clock`/`Exec` — фейки.

### 5.4. vless (парсер ссылки) — ЧИСТЫЙ

- **Назначение:** `ParseLink(raw) (ServerProfile, error)`, без I/O.
- **Ключевые типы:** `ServerProfile`, `SecurityType`, `TransportType`, типизированные ошибки (`ErrNotVLESS`, `ErrMissingUUID`, `ErrInvalidUUID`, `ErrMissingHost/Port`, `ErrInvalidPort`, `ErrMissingRealityKey`, `ErrUnsupportedTransport/Security`, `ParseError`).
- **Внешние зависимости:** только stdlib (`net/url`, `net`, `strings`, `strconv`, `errors/fmt`).
- **Тестовые швы:** функция чистая — table-driven, без моков.

### 5.5. singbox (генератор конфигов) — ЧИСТЫЙ

- **Назначение:** `GenerateProxyConfig(p, physIface)` и `GenerateForwarderConfig(p, physIface)` + `MarshalIndented`.
- **Ключевые типы:** `Config`, `DialerOptions{BindInterface}`, `RouteOptions{DefaultInterface, AutoDetectInterface, DefaultDomainResolver, Final, Rules}`, `RouteRule`.
- **Внешние зависимости:** только stdlib (`encoding/json`); НЕ импортирует sing-box (контракт = JSON-форма).
- **Тестовые швы:** golden-файлы; чистые функции.

### 5.6. netstate (пассивный детектор Cisco)

- **Назначение:** `Observe(ctx) (NetState, error)` — read-only снимок: кто владеет default route, foreign tunnels, наличие Cisco-процессов; публикует `DefaultRouteIface` И физ. egress (см. раздел 10, defect physiface).
- **Ключевые типы:** `NetState`, `TunnelIface`, `Detector`, `CommandRunner`, `InterfaceLister`, `Clock`, `IfaceInfo`, `ProcInfo`, `DefaultRoute`.
- **Внешние зависимости:** stdlib + read-only macOS CLI (абсолютные пути: `/sbin/ifconfig`, `/usr/sbin/netstat`, `/sbin/route`, `/bin/ps`). НИКОГДА Cisco-бинарники.
- **Тестовые швы:** `CommandRunner` (fakeRunner по argv), `InterfaceLister`, `Clock`; чистые парсеры на фикстурах.
- **Вердикт «cisco-passive»: `holds`.** Пассивная детекция подтверждена на живой машине; различение нашего TUN от Cisco — по инъектируемому имени устройства + точному `/30` (рекомендуется `198.18.0.0/30`, НЕ `172.18/16`). FP/FN охарактеризованы (process != connected; split-tunnel — known limitation).

### 5.7. policy (движок решений) — ЧИСТЫЙ

- **Назначение:** `Decide(in DecideInput) DecisionResult` — 100% чистая функция.
- **Ключевые типы:** `Mode`, `CiscoState`, `UserIntent`, `Action`, `DecideInput`, `DecisionResult`.
- **Внешние зависимости:** только stdlib (никаких импортов в `policy.go`).
- **Тестовые швы:** ни одного мока — полный кросс-продукт.

### 5.8. monitor (цикл опроса)

- **Назначение:** `Monitor.Run(ctx)` — опрос `Detector` по тикеру (~2с), debounce флапа Cisco, публикация `Event{NetState, DecisionResult}`.
- **Ключевые типы:** `Detector`, `Clock`/`Ticker`, `Debouncer`, `Event`.
- **Внешние зависимости:** через `Detector` и `Clock` (фейки).
- **Тестовые швы:** `mockDetector` (scripted), `fakeClock`/`fakeTicker`.

### 5.9. ui (TUI Bubble Tea) — ЧИСТЫЙ reducer

- **Назначение:** три экрана (link-input, dashboard, modal); `Update(msg)` без I/O и без policy-логики.
- **Ключевые типы:** `Model`, `screen`, `uiMode`, msg-типы (`netStateMsg`, `coreStatusMsg`, `errorMsg`, `policyVerdictMsg`, `linkParsedMsg`, ...); порты `PolicyEngine`/`LinkParser`/`CoreController`/`Clock`.
- **Внешние зависимости:** `charmbracelet/bubbletea`/`bubbles`/`lipgloss`; sibling-пакеты только через интерфейсы.
- **Тестовые швы:** все порты инъектируются; `teatest` для e2e-потока; логические ассерты на поля `Model`.

---

## 6. Политика и монитор

### Машина состояний

- **Mode:** `ModeProxy` (по умолчанию, proxy всегда поднят), `ModeVPN` (форвардер тоже поднят), `ModeSwitching` (транзит).
- **CiscoState:** `CiscoUnknown` (только на первом наблюдении), `CiscoInactive`, `CiscoActive`.
- **UserIntent:** `IntentNone`, `IntentEnableVPN`, `IntentDisableVPN`.
- **Action:** `ActNone`, `ActStartForwarder`, `ActStopForwarder`, `ActShowWarning`, `ActRefreshProxy`, `ActNotify`, **`ActFailClosed`** (D6 — мгновенный дроп форвардинга перед сносом TUN).

### Таблица переходов (Cisco↑ = Inactive→Active, Cisco↓ = Active→Inactive)

| Mode | Событие/Intent | Cisco | Actions | NextMode |
|------|----------------|-------|---------|----------|
| ModeProxy | IntentEnableVPN | Active (steady) | `[ActShowWarning]` (правило a) | ModeProxy |
| ModeProxy | IntentEnableVPN | Inactive | `[ActStartForwarder]` | ModeSwitching→ModeVPN (по подтверждению) |
| ModeVPN | — (наблюдение) | **Cisco↑** | `[ActFailClosed, ActStopForwarder, ActNotify]` (правило b, fail-closed) | ModeProxy |
| ModeProxy | — (наблюдение) | **Cisco↓** | `[ActRefreshProxy]` (правило c) | ModeProxy |
| ModeVPN | IntentDisableVPN | любое | `[ActStopForwarder]` | ModeProxy |
| ModeProxy | IntentDisableVPN | любое | `[ActNone]` | ModeProxy |
| ModeVPN | — | Active (steady) | `[ActNone]` | ModeVPN |
| ModeProxy | — | Inactive (steady) | `[ActNone]` | ModeProxy |
| любой | — | переход в/из `CiscoUnknown`, IntentNone | `[ActNone]` (без spurious на старте) | без изменений |
| ModeSwitching | любой Cisco edge, IntentNone | — | `[ActNone]` (инертен в транзите) | ModeSwitching |
| ModeProxy | смена физ. интерфейса (en0↔en6) | — | `[ActRefreshProxy]` (re-bind) | ModeProxy |

Правило (a): VPN при активном Cisco — только предупреждение, НИКОГДА `ActStartForwarder`. Правило (b): авто-падение в proxy при появлении Cisco. Правило (c): re-dial upstream при уходе Cisco, БЕЗ закрытия слушателей (ограничение — раздел 10).

### Модель детекции (финал — D5/D6): событийная + fail-closed

- **Основной триггер — событийный:** слушаем `PF_ROUTE`-сокет (`AF_ROUTE`) и/или `SCDynamicStore` (ключи `State:/Network/Global/IPv4` и интерфейсные). Появление/исчезновение чужого `utun`/маршрута приходит за миллисекунды, а не за 2с.
- **Fail-closed:** на событие «появился чужой туннель» в `ModeVPN` сразу `ActFailClosed` (стоп форвардинга/дроп) → затем `ActStopForwarder`. Прикладной трафик в окне гонки не туннелируется через VLESS.
- **`Detector` получает второй источник:** `EventSource interface { Events(ctx) <-chan struct{} }` (реальная реализация — route-socket; фейк — управляемый канал). На каждое событие выполняется тот же `Observe`+`Decide`, что и по тику. Поллинг ниже остаётся как страховка от пропущенного события.

### Каденс опроса и debouncing (поллинг-fallback)

- **Тик:** ~2с. На каждый тик `Detector.Observe(ctx)` с per-tick таймаутом (~1.5с < тика); при ошибке/таймауте тик пропускается, удерживается предыдущее committed-состояние.
- **Debounce:** требуется N подряд одинаковых наблюдений Cisco (по умолчанию 2, ≈4с) перед коммитом флипа — подавляет флап utun при (пере)подключении Cisco. Только committed-переходы доходят до `Decide`.
- **Открытый вопрос:** возможно асимметричный debounce (коммитить `Active` быстро, debounce только `Active→Inactive`), чтобы авто-падение (правило b) было быстрее.
- **Важно (defect physiface, см. раздел 10):** change-detection должна ключаться на ФИЗИЧЕСКИЙ egress (`PhysicalIface`), а не только на `DefaultRouteIface` — иначе смена Wi-Fi↔Ethernet под поднятым нашим TUN не детектится.

---

## 7. Парсинг VLESS и генерация конфигов

### Модель ServerProfile

```
ServerProfile {
    UUID      string
    Host      string        // bare host или IP, IPv6 БЕЗ скобок
    Port      uint16
    Name      string        // из URL fragment, percent-decoded
    Flow      string
    Security  SecurityType  // SecurityNone | SecurityTLS | SecurityReality
    TLS       TLSParams     { ServerName, Fingerprint string; ALPN []string; Insecure bool }
    Reality   RealityParams { Enabled bool; PublicKey, ShortID string }
    Transport TransportParams { Type TransportType; ServiceName string;  // grpc
                                Path string;                              // ws/http/xhttp
                                Host []string; HeaderType string;
                                Mode string; Extra string }             // xhttp-only
    // (опционально) Raw string — исходная ссылка для дебага/отображения в TUI
}
```

`ParseLink` использует `net/url` + `net.SplitHostPort` + `url.QueryUnescape`. Обрабатывает `security=reality|tls|none`, `type=grpc|ws|http|tcp|xhttp` (алиас `type=splithttp`, Xray'ево старое имя transport'а, нормализуется в `xhttp`), параметры `pbk/sid/sni/fp/flow/alpn/host/path/serviceName/headerType/encryption`. Нормализует IPv6 в скобках и percent-encoding. По умолчанию `type=tcp`, `encryption=none`. Неизвестный transport → `ErrUnsupportedTransport` (не молчаливая ошибка). Ключи запроса матчатся case-insensitive где безопасно (`serviceName`/`servicename`).

**`type=xhttp` (Xray XHTTP, экс-SplitHTTP)** — добавлен на замену gRPC/WS, которые РКН научился блокировать; sing-box апстрим этот transport не реализует, поэтому singctl регистрирует собственный тип аутбаунда `vless-xhttp` в `internal/singboxext` (см. «Архитектура» в README.md) и реализует клиент в `internal/xhttp`. Два дополнительных link-параметра, оба распознаются только когда `type=xhttp`:
  - `mode=auto|packet-up|stream-up|stream-one` — режим аплинка Xray; `auto` резолвится как `packet-up`, либо как `stream-one` под REALITY (как в самом Xray). По умолчанию `auto`.
  - `extra=<JSON>` — сырой блок `xhttpSettings` Xray (padding placement/method, session/seq placement, uplink data placement, `scMaxEachPostBytes`, `scMinPostsIntervalMs`, `headers`, …), копируется в конфиг как есть; невалидный JSON → `ErrInvalidXHTTPExtra`.

  Пример ключа:
  ```
  vless://0b1e2c3a-9f4d-4a1b-8e2f-7c6d5a4b3c2d@edge.example.invalid:443?security=reality&pbk=Xk3f9pQvW2s7rY1zN8mB4hC6dE0aJ5tL9oU2iP7qR3s&sid=a1b2c3d4&sni=www.microsoft.com&fp=chrome&type=xhttp&mode=packet-up&path=%2Fxh&extra=%7B%22scMaxEachPostBytes%22%3A1000000%7D#xhttp-example
  ```

  Известные ограничения (единое место, остальные документы на него ссылаются, не повторяют):
  - **HTTP/3 (`alpn=h3`) не поддержан** — ключ с таким ALPN отклоняется при старте с явной ошибкой (`internal/xhttp` возвращает её из `New`).
  - **`xmux` и `downloadSettings` (раздельные up/down-линки) внутри `extra=` игнорируются** — это server-side/специфичные для их собственного мультиплексора knobs, на то, что клиент кладёт на провод, не влияют.
  - **XTLS Vision (`flow=`) не эмитится для xhttp-ключей** — сам Xray запрещает flow на не-raw transport'ах.
  - **App Store SKU (сэндбоксовый `NEPacketTunnelProvider`, см. [docs/appstore-sku.md](docs/appstore-sku.md)) не может использовать xhttp-ключи** — та сборка линкует стоковый `Libbox.xcframework`, который хардкодит родной реестр аутбаундов sing-box, и туда наш `vless-xhttp` зарегистрировать нельзя; генерация конфига для этого SKU падает с явной ошибкой. Developer-ID приложение и CLI-демон не затронуты.

  Верификация: `internal/xhttp` — round-trip тесты против тестового XHTTP-сервера внутри пакета; `internal/core/xhttp_e2e_test.go` (build-теги `integration singbox`, пропускается без `XRAY_BIN`) гоняет весь стек против РЕАЛЬНОГО Xray-core сервера для packet-up/stream-up/stream-one по HTTP/1.1 и HTTP/2, плюс кейс с REALITY.

### Производство двух конфигов

Обе функции чистые: `func(p ServerProfile, physIface string) (Config, error)`. `physIface` инъектируется как строка (детект — задача runtime/netstate).

**PROXY config (`GenerateProxyConfig`):**
- inbounds: ровно socks `1080` + http `2080`;
- outbounds: `vless` (Reality/gRPC к `193.188.22.147:443`) и `direct` (ru/private); `bind_interface = physIface` ставится на оба ТОЛЬКО когда `physIface != ""`, а это передаётся лишь в VPN-режиме (D3). В proxy-only режиме `physIface=""` → bind НЕ ставится, трафик идёт по дефолтному маршруту (через Cisco, если активен — обход невозможен, D4);
- `route.default_interface = physIface`, **НЕ** `auto_detect_interface` (конфликт — см. раздел 3/10);
- DNS: `type:https`(detour=proxy) + `type:local`, `default_domain_resolver=local`;
- route rules: `sniff`, `ip_is_private → direct`, ru/IDN `domain_regex → direct`, `final = proxy`;
- ноль inbound типа `tun` (PROXY никогда не открывает TUN — codified тестом);
- если `physIface == ""` — поля `bind_interface`/`default_interface` опускаются (omitempty), функция не паникует (для покрытия обоих путей). Инвариант «непустой physIface перед входом в VPN» энфорсит оркестратор.

**FORWARDER config (`GenerateForwarderConfig`):**
- inbound: `tun` (адрес `198.18.0.1/30` + IPv6 `/126`, `auto_route:true`, `strict_route:true`, `stack:"system"`) — **подсеть смещена из `172.18/16` в `198.18.0.0/30`**, чтобы исключить коллизию с пулом Cisco (наблюдалось `172.18.113.59/22`);
- outbound: ОДИН socks → `127.0.0.1:1080` (форвардер только релеит, vless в нём НЕТ);
- `route.auto_detect_interface = true`;
- route rule: `hijack-dns`, scope `inbound=[tun-in]`;
- `final = socks-out`;
- **Фоллбэк-правило loop-safety (по вердикту «loop»):** route-rule на форвардере, отправляющий `193.188.22.147/32`, физ. gateway и loopback в `direct`/bypass.

`MarshalIndented` — тонкая обёртка `encoding/json` для golden-тестов; маршалинг детерминирован (фиксированный порядок полей, без map-итерации).

**Замечание по артефактам:** существующие `config.json`/`config-vpn.json` НЕ содержат `bind_interface`/`default_interface`, а `config-vpn.json` — single-instance с `auto_detect_interface=true`. Golden-файлы намеренно расходятся (добавляют bind-поля, два инстанса) — это redesign; нужно регенерировать golden из того же генератора и задокументировать расхождение.

---

## 8. Стратегия unit-тестирования

### Инъектируемые интерфейсы / фейки (полный список)

- `core.Core` / `core.Factory` → `FakeCore` (records Start/Close/Reload, StartErr/CloseErr/ReloadErr, CrashCh).
- `runtime.CoreFactory`, `runtime.RouteController`, `runtime.InterfaceProber`, `runtime.Clock`/`Ticker`, `runtime.ExecRunner` → `fakeRouteController`, `fakeInterfaceProber`, `fakeClock`, `fakeExecRunner`.
- `platform.Exec`/`FS`/`Clock`, `platform.RealUser` (через инъекцию `env`+`UserLookup`).
- `netstate.CommandRunner` (`fakeRunner` по argv), `InterfaceLister` (`fakeLister`), `Clock`.
- `monitor.Detector` (`mockDetector` scripted), `Clock`/`Ticker`.
- `ui` порты: `PolicyEngine`, `LinkParser`, `CoreController`, `Clock`; канал `netCh` (в т.ч. закрытый).
- `osiface.*` (если выделяется общий слой): `CommandRunner`, `FS`, `InterfaceLister`, `RouteController`, `ProcessLister`, `Clock`/`Ticker`.
- `testutil.WorldState` — связывает все фейки в одну симулированную машину.

### Table-driven тесты

- `vless.ParseLink`: gRPC/Reality, ws/TLS+Host header, tcp/plain, IPv6-host, IDN-fragment + percent-encoding, полная таблица ошибок (`errors.Is`).
- `policy.Decide`: полный кросс-продукт (Mode × PrevCisco × NewCisco × Intent); правила (a)/(b)/(c); `CiscoUnknown`-подавление; инертность `ModeSwitching`; детерминизм/идемпотентность.
- `netstate` парсеры (route/netstat/ifconfig), `classifyTunnels`, `detectCiscoProcess`.
- `monitor.Debouncer`: подавление одиночного флапа, коммит после порога.

### Golden / fixture-файлы

- **Generated golden:** `singbox/testdata/proxy.golden.json`, `forwarder.golden.json` (+ `configgen/testdata/golden/proxy_instance.json`, `tun_forwarder.json`). Канонизация (sorted keys, фикс. indent) перед сравнением; флаг `-update`. Ассерты: на каждом outbound `bind_interface`, `route.default_interface`, отсутствие `auto_detect_interface` на PROXY, inbounds = socks `1080` + http `2080`, отсутствие `tun` в PROXY, форвардер = socks→`1080` + `hijack-dns` + `198.18.0.x/30`.
- **Captured OS fixtures** (`netstate/testdata/`, `testutil/testdata/fixtures/`): `cisco_connected_ifconfig.txt` (utun4 flags=80d1 NOARP inet 172.18.113.59/22 + link-local utun0-3), `cisco_connected_netstat_rn.txt` (`default link#22 utun4`), `cisco_connected_route_default.txt` (utun4), `cisco_disconnected_*`, `no_cisco_*`, `our_tun_up_*`, `ps_cisco.txt`, `scutil_*`.

### Симуляция переходов Cisco и сетевых изменений

- `testutil.WorldState` + хелперы `CiscoConnected()`, `CiscoDisconnected()`, `NetworkChanged(newPhysIface)` атомарно мутируют состояние, позволяя одному тесту прогнать connect→disconnect или Wi-Fi-свитч.
- `Monitor.Run` с `fakeClock`/`fakeTicker` + `mockDetector` (scripted очередь): проверка эмиссии правил (b)/(c) только после debounce, отсутствие события на blip, смена `PhysicalIface`, чистый shutdown по ctx-cancel, устойчивость к ошибке Detector.
- `runtime` Manager: `TestStartForwarder_DoesNotTouchProxyCore`, `TestStopForwarder_ClosesOnlyForwarder_AndCleansOurTunOnly`, `TestCleanupOrphans_*`, `TestRefreshProxy_*`, `TestStartForwarder_StartFailure_RollsBackRoutes`, crash через `CrashCh`.

### Guardrails

- `TestNoForbiddenImports`: статическая проверка графа импортов — вне `osiface/real_*.go`/`core/real_core.go` никто не импортирует `os/exec` или sing-box `box`; нигде не импортируется путь Cisco. Делает «никогда не трогаем Cisco / полностью mockable» структурной гарантией.
- `TestFakeCommandRunner_UnscriptedArgvFails`: не-заскриптованный argv → явная ошибка (нет тихого реального exec).

### Цель покрытия

- Целевое покрытие **логики/решений**: 100% по чистым пакетам (`vless`, `singbox`, `policy`) и высокое по оркестрации (`runtime.Manager`, `monitor`, `netstate.Classify`, `ui.Update`).
- Реальные адаптеры (`boxcore.go`/`real_core.go`, `osiface/real_darwin.go`) намеренно ИСКЛЮЧЕНЫ из дефолтного прогона (тонкие шимы, integration-теги).

### Гарантия запуска без root / без реального sing-box / без Cisco

- **Дефолтный `go test ./...` обязан быть герметичным:** ВСЕ тесты, импортирующие реальный sing-box (`TestDecodeOptions_*`, golden-тесты, валидируемые против реальной схемы `option.Options`), переносятся за `//go:build integration`. CI-джоба проверяет, что дефолтный прогон (без тега integration) проходит fake/stdlib-only и не падает на резолве toolchain/библиотеки. Это устраняет overstatement из вердикта «testability» (claim `uncertain`): «FULLY / every subsystem» переформулируется как «вся логика решений каждой подсистемы unit-тестируема через фейки без root/тоннеля/Cisco; тонкие реальные адаптеры — только в integration-наборе».

### Manual / integration чек-лист (то, что НЕ покрывается unit-тестами; требует root + живую машину, build-tagged)

1. **Loop-safety (HARD):** при поднятом TUN-форвардере трафик PROXY к `193.188.22.147` уходит через `en0`, а не utun — проверить per-interface пакетным счётчиком. Отдельно проверить `direct`-outbound и UDP/DNS-сокеты.
2. **Два бокса сосуществуют** в одном процессе без паники на дубль-регистрации.
3. **Graceful Close восстанавливает таблицу маршрутов** (kernel падает на физ. default), причём при живом Cisco utun4 — подтвердить, что выбирается `en0`, а не stale-запись.
4. **SIGKILL-тест:** `CleanupOrphans` реально освобождает orphan `198.18.0.x/30` utun, НЕ трогая utun4/`172.18.112/22`.
5. **Listener continuity:** RefreshProxy/Reload — наблюдать, реально ли рвутся `1080/2080` (unit-тесты видят только счётчики FakeCore, не реальный сокет).
6. **TUN readiness signal:** какой сигнал доказывает, что TUN полностью поднят (poll маршрута / start-callback / clock-таймаут).
7. **Live Cisco disconnect:** проверить, что Cisco всегда чисто сносит utun4 (а не оставляет half-up при reconnect-флапе); проверить subnet-only split-tunnel FN.
8. **Bind на iPhone USB (en7)/cellular-tether** ведёт себя как на en0.
9. **`acsockext` passthrough при ВЫКЛЮЧЕННОМ Cisco:** подтвердить, что en0-bound (escape нашего proxy из TUN) работает, когда Cisco disconnected, несмотря на загруженный сокет-фильтр Cisco. Критично для VPN-режима — при активном Cisco такой bind заблокирован (проверено 2026-06-02).

---

## 9. Этапы реализации

Каждый этап имеет test-gate — unit-тесты, которые должны пройти ПЕРЕД переходом дальше.

**Этап 0 — Bootstrap.** Установить Go ≥1.23, Xcode CLT (clang есть). Создать `go.mod` (module path, пин sing-box v1.12.x), `Makefile`, `README-build.md`. Запинить `go.sum`.
*Test-gate:* `go build ./...` компилируется; `go test ./...` (пустой) проходит; `TestNoForbiddenImports` каркасно работает.

**Этап 1 — Чистое ядро: vless + singbox.** `ServerProfile`, `ParseLink`, `errors.go`; `schema.go`, `GenerateProxyConfig`/`GenerateForwarderConfig`, `MarshalIndented`, golden-файлы.
*Test-gate:* все table-тесты `ParseLink` (включая таблицу ошибок) + golden-тесты обоих конфигов проходят; `TestProxyConfig_NoTunInbound_LoopGuard`, `TestGenerateProxyConfig_EmptyPhysIface_OmitsBind`, `TestRoundTrip_ParseThenGenerate`, `TestMarshalStability_Deterministic`.

**Этап 2 — core seam + Manager (на фейках).** `Core`/`Factory`/`FakeCore`; `runtime.Manager`, `types.go`, `clock.go`, `exec.go`, `fakes_test.go`. RealCore/`boxcore.go` пишется, но за integration-тегом.
*Test-gate:* `TestManager_StartProxy_*`, `EnableVPN/DisableVPN/DoubleEnableVPN/Shutdown/TunCrash`, `ReloadProxy`, `StartForwarder_StartFailure_RollsBackRoutes`, `StartProxy_Idempotent_AndRunsCleanupFirst`.

**Этап 3 — netstate (пассивный детектор).** Парсеры route/netstat/ifconfig, `classifyTunnels`, `detectCiscoProcess`, `Observe`. Captured fixtures.
*Test-gate:* `TestObserve_CiscoConnected/Disconnected/NoCisco/OurTunUp/OurTunAndCiscoBoth`, `TestParseRouteGetDefault`, `TestParseNetstatDefaults_ExcludesLinkLocalV6`, `TestParseIfconfig_FlagsAndNoARP`, `TestClassify_TruthTable`. Реализовать публикацию `PhysicalIface` (см. defect physiface).

**Этап 4 — policy + monitor.** `Decide` (чистый), `Debouncer`, `Monitor.Run`, событийный `EventSource` (PF_ROUTE/SCDynamicStore) и `ActFailClosed` (D5/D6). Консолидировать `NetState`/`CoreStatus` в `internal/types`.
*Test-gate:* полный кросс-продукт `Decide`; `TestDebouncer_*`; `TestMonitor_EmitsRuleBEvent/RuleCEvent`, `NoEvent_WhenUnchanged`, `DefaultRouteIfaceChange/PhysicalIfaceChange_TriggersRefresh`, `StopsCleanly_OnContextCancel`, `DetectorError_DoesNotCrashLoop`, `TestMonitor_EventSource_TriggersFailClosedBeforeStop`, `TestDecide_CiscoUp_InVPN_EmitsFailClosedThenStop`.

**Этап 5 — runtime prober/routecontroller + интеграция physIface.** `InterfaceProber.PhysicalDefault` (игнор Cisco global primary), `RouteController.OrphanTunRoutes` (только наш `/30`).
*Test-gate:* `TestPhysicalDefault_IgnoresCiscoGlobalPrimary`, `TestPhysicalDefault_NoCisco_PicksActiveHardwareService`, `TestRouteController_OrphanTunRoutes_NeverMatchesCisco`, `TestBuildProxyOptions_SetsBindInterfaceOnOutbounds`, `TestBuildForwarderOptions_SocksOutboundTo1080`, `TestNetworkChange_RebindPhysicalInterface`.

**Этап 6 — TUI.** `Model`/`Update`/`View`, msg-типы, commands, ports.
*Test-gate:* `TestLinkSubmitted_*`, `TestToggleToVPN_WhileCiscoActive_ShowsModal`, `TestToggleToVPN_WhenAllowed_StartsTunAndConfirms`, `TestNetStateMsg_*` (включая AutoSwitch и RefreshUpstream без рестарта proxy), `TestModalDismiss_*`, `TestMonitorChannelClosed_EmitsErrorNotHang`, `TestQuitKey_*`, e2e `teatest`, `TestWindowResize_*`.

**Этап 7 — main + persistence + sudo.** `cmd/singctl/main.go` (preflight root, signal-cancel, wiring), `profile.Store`, `platform.RealUser`.
*Test-gate:* `TestMain_Preflight_RequiresRoot`, `TestRealUser_ResolvesFromSudoUser`, `TestProfileStore_WritesUnderRealUserHome_AndChowns`. Полный дефолтный `go test ./...` зелёный без integration-тега.

**Этап 8 — Integration (build-tagged, под root, на живой машине).** Прогон manual/integration чек-листа (раздел 8): loop-safety пакетным счётчиком, два бокса, graceful Close, SIGKILL+CleanupOrphans, listener continuity, TUN readiness, live Cisco disconnect.
*Gate:* интеграционный набор проходит на реальном железе; HARD-пункты loop/listener подтверждены ИЛИ задокументированы как принятый компромисс.

---

## 10. Риски и открытые вопросы

Риски выведены прежде всего из вердиктов верификации. Для каждого вердикта, который НЕ `holds`, ниже явный риск с митигацией.

### Риски из вердиктов

**R1 — Routing loop не доказан полностью (вердикт «loop»: `uncertain`).** `bind_interface`/`default_interface` надёжно покрывают VLESS-сокет, но привязка `direct`-outbound и local-DNS/UDP НЕ подтверждена; есть внутреннее противоречие «set once» vs «PROXY через Cisco utun4».
*Митигация:* привязать ВСЕ outbounds (vless+direct) + `route.default_interface`; для DNS — bound dialer или весь DNS через `detour=proxy`; **фоллбэк-route-rule на форвардере** (VLESS-IP/gateway/loopback → bypass); НЕ ставить `auto_detect_interface` на PROXY; интеграционный пакетный тест перед доверием claim. Severity: **high**. *Решение ревью (D3/D4):* `bind_interface`/`default_interface` ставятся ТОЛЬКО в VPN-режиме (Cisco гарантированно выключен) — противоречие «set once» снято; в proxy-only bind не ставится (обход Cisco невозможен, D4). Осталось интеграционно подтвердить, что bind escape-ит НАШ tun при ВЫКЛЮЧЕННОМ Cisco (фильтр `acsockext` в passthrough).

**R2 — Гарантия «слушатели 1080/2080 никогда не падают» опровергнута для случая Cisco-off (вердикт «seamless»: `refuted`).** В sing-box нет in-place reload; `Reload = Close+New+Start` рвёт слушателей на доли секунды. Это противоречит безусловной формулировке. Часть «toggle VPN не роняет слушателей» — держится (два независимых бокса).
*Митигация:* переформулировать гарантию — оставить сильную для VPN-toggle; для Cisco-off RefreshProxy либо (a) принять sub-second gap с rebind на те же `1080/2080` (`SO_REUSEADDR`, локальные сокеты приложений получают reset и переподключаются), либо (b) ПЕРЕД пином sing-box эмпирически проверить, есть ли поддержанный способ re-dial одного outbound без recreate бокса. Согласовать механизм между skeleton/runtime/testing и закодировать в `Core`. Добавить integration-тест на выживание listener fd. Рассмотреть connect-on-demand outbounds, чтобы вообще убрать RefreshProxy. Severity: **high**. *Решение ревью (D3):* sub-second разрыв принят. Сильная гарантия — только для VPN-toggle (два бокса); для Cisco-off RefreshProxy = пересоздание бокса с rebind на те же `1080/2080`.

**R3 — Детект физ. интерфейса при поднятом TUN опровергнут (вердикт «physiface»: `refuted`).** Монитор ключуется на `DefaultRouteIface`, но при нашем TUN, владеющем default, это значение постоянно; смена Wi-Fi↔Ethernet под TUN НЕ детектится → PROXY привязан к мёртвому `en0`, а `strict_route` глушит весь egress без ошибки. Плюс гонка: physIface пробится независимо в Manager и в Monitor. Плюс grounding опирается на Cisco SC-service, тогда как наш utun — raw kernel device.
*Митигация:* добавить `PhysicalIface` в единый `NetState` (консолидировать три расходящихся определения в `internal/types`); вычислять через `route -n get -ifscope <candidate> default`, исключая наш utun и foreign tunnel; триггерить RefreshProxy/rebind на смену `PhysicalIface` НЕЗАВИСИМО от `DefaultRouteIface`. Единый источник истины physIface (владеет Monitor, Manager читает). Рассмотреть event-driven PF_ROUTE/SCDynamicStore вместо polling. Integration-тест с реальным TUN. Severity: **high**. *Решение ревью (D5):* детекция событийная — смена интерфейса и появление Cisco ловятся через PF_ROUTE/SCDynamicStore, не только поллингом; единый `PhysicalIface` в `internal/types`.

**R4 — Close sing-box не восстанавливает таблицу маршрутов при crash (вердикт «embedding»: `uncertain`).** `unsetRoutes()` удаляет только добавленные sing-tun маршруты, не снимает снапшот; при SIGKILL остаётся orphan utun и stale default. Плюс рецепт встраивания в дайджесте содержал API-ошибки.
*Митигация:* переформулировать гарантию Close (только graceful); приоритизировать `CleanupOrphans`/`OrphanTunRoutes` на старте + best-effort signal handler; исправить `boxcore.go` (`box.Context` с 6 аргументами вкл. `ServiceRegistry`; `box.Options{Context, Options}` с embedded `option.Options`). Severity: **medium**.

**R5 — overstatement «FULLY unit-tested» (вердикт «testability»: `uncertain`).** Реальные адаптеры и тесты, импортирующие sing-box, не в дефолтном прогоне; behavioral-корректность (loop/listener/TUN readiness) — integration-only.
*Митигация:* переформулировать claim (логика решений vs тонкие адаптеры); перенести ВСЕ sing-box-импортящие тесты за `//go:build integration`; CI-джоба на герметичный дефолтный прогон; integration-чек-лист для трёх непокрываемых поведений; регенерировать golden из того же генератора. Severity: **medium**.

### Прочие риски (severity по дайджесту)

- **Address-space коллизия** нашего TUN с пулом Cisco `172.18/16` — *high*. Митигация: подсеть `198.18.0.0/30`, ownership по имени устройства + точному `/30`, никогда по `172.18` префиксу.
- **API-дрейф sing-box 1.11↔1.12** (Endpoints, типизированный DNS, `route.action`) — *high*. Митигация: пин ОДНОЙ версии **v1.12.x** (под существующую 1.12-схему конфигов), изоляция в `boxcore.go`, валидация `TestDecodeOptions` против конфигов в CI (integration).
- **CGO/«статический бинарь»** — *medium*. Митигация: цель = self-contained бинарь (без внешней загрузки), `CGO_ENABLED=1` + `with_gvisor`, gVisor как фоллбэк.
- **Stale `default_interface` при сетевой смене** — *medium* (пересекается с R3).
- **Дубль-регистрация реестров двух боксов** — *medium*. Митигация: регистрировать один раз package-level, шарить read-only.
- **Split-tunnel Cisco FN** — *medium*. Митигация: NOARP+IPv4+process-эвристика, консервативное смещение (при сомнении — запрещать VPN).
- **`acsockext` (сокет-фильтр Cisco) остаётся загружен и при выключенном Cisco** — *medium* (новое, из теста 2026-06-02). При АКТИВНОМ Cisco фильтр блокирует en0-bound к туннелируемым адресам (подтверждено). Нужно интеграционно проверить, что при ОТКЛЮЧЁННОМ Cisco фильтр пропускает en0-bound — иначе loop-avoidance в VPN-режиме (escape из нашего TUN через bind) сломается. Митигация: пункт чек-листа §8; fallback — `route_exclude` на форвардере / gVisor.
- **FP от системных utun0-3** — *high* (но в дизайне закрыт): исключать link-local-only и fe80::-gateway v6-default; требовать IPv4-default или NOARP+IPv4.
- **Имя нашего utun неизвестно до старта** — *medium*. Митигация: launcher читает реальное имя и зовёт `SetOurTun(name, addr)` до оценки политики; в окне — fail-safe (foreign), сглаживается debounce.
- **Action ordering/atomicity** (правило b: `[StopForwarder, Notify]`) — *medium*. Митигация: фикс. порядок слайса, Mode → NextMode только после успеха StopForwarder.
- **Root-owned файлы** — *low*. Митигация: резолв `SUDO_USER` + `FS.Chown`.
- **Golden churn / locale-дрейф парсеров** — *low*. Митигация: канонизация JSON; парсеры по именам полей, не по колонкам; предпочесть `net.Interfaces()`/routing socket тексту.

### Открытые вопросы для пользователя

**Разрешено в ревью (детали в §0):** 1 — пин v1.12.x; 2 — `module singctl`, локально; 3 — sub-second разрыв принят, bind только в VPN; 4 — обход Cisco НЕВОЗМОЖЕН (проверено 2026-06-02, см. D4), опция `--proxy-bind` убрана, proxy-only всегда через Cisco; 6 — подсеть `198.18.0.0/30`; 7 — детекция событийная (PF_ROUTE/SCDynamicStore) + поллинг-fallback; 10 — только arm64. Плюс добавлен `ActFailClosed` (fail-closed teardown, D6). Остальные ниже остаются техническими на этап реализации.

1. **Версия sing-box:** пинить **v1.12.x** (под 1.12-схему `config.json`/`config-vpn.json`: типизированный DNS `type`, `route.action`)? Под 1.11 текущие конфиги НЕ декодятся.
2. **Module path:** `github.com/mikhailvlasov/singctl` или иной? Влияет на все `internal/`-импорты.
3. **Listener gap при RefreshProxy:** приемлем ли sub-second gap (whole-box recreate, rebind на те же `1080/2080`), или искать no-recreate путь (sing-box нативно не поддерживает)?
4. ✅ **РЕШЕНО (D4):** обход Cisco на en0 невозможен (эмпирически, сокет-фильтр `acsockext`) → в proxy-only режиме туннелируем через Cisco; `bind_interface` ставится только в VPN-режиме. Развилка R1 закрыта.
5. **bind_interface vs route.default_interface:** ставить оба (defensive) или достаточно одного на macOS? Подтвердить минимальную корректную комбинацию на реальном TUN.
6. **TUN-подсеть:** подтвердить переход на `198.18.0.0/30` (вместо `172.18.0.1/30`).
7. **Триггер смены физ. интерфейса:** отдельный `ActRebindInterface` или переиспользовать `ActRefreshProxy`? Event-driven (PF_ROUTE/SCDynamicStore) или строго poll?
8. ✅ **РЕШЕНО (ревью Этапа 1):** форвардер ИМЕЕТ свой минимальный `dns`-блок и хайджекает DNS — в sing-box 1.12 hijacked-DNS отвечает собственный DNS-модуль инстанса, а НЕ проксируется. Сервер форвардера: DoH `1.1.1.1` с `detour=socks-out` → резолв по TCP через прокси/VLESS (leak-proof, без зависимости от хрупкого SOCKS5 UDP-associate), `strategy=ipv4_only`.
9. ✅ **РЕШЕНО (ревью Этапа 1):** ВСЁ гонится в socks→`1080` (PROXY — единый источник ru/private-сплита по SNI-сниффу), НО локально на форвардере добавлен `ip_is_private→direct` для короткого замыкания LAN/mDNS/принтеров (без round-trip через прокси).
10. **Universal (arm64+amd64) бинарь** или только arm64 (машина Apple Silicon)?
11. **Debounce/каденс:** 2с тик + 2-sample (≈4с лаг) приемлем для авто-падения (b), или нужен асимметричный debounce?
12. **Контракт Executor↔Monitor:** Executor подтверждает завершение Start/StopForwarder обратно в state-machine (Mode advance), или Monitor оптимистично ставит NextMode?
13. **Зависимость afero** для `FakeFS` допустима, или нужен zero-dep map-based FS?
14. **Skip link-input при сохранённом профиле** (зависит от подсистемы persistence) — нужен ли?
15. **Поведение при dial-ошибке в ModeVPN** (TUN поднят, upstream упал): inline-ошибка или авто-падение в proxy?
16. **Teardown при quit:** graceful shutdown core (stop tun, оставить proxy? остановить оба) — порядок.
