# Spec: protocol modules and engine adapters

Status: approved, being implemented. Written 2026-09-07.

## Why

singctl grew from one protocol to eight, and adding each one meant editing the
same three places: the parser dispatcher, the schema, and the generator's
`switch`. WireGuard does not fit that shape at all — it arrives as an INI config
file rather than a share link, and sing-box models it as an *endpoint* rather
than an outbound. AmneziaWG does not even fit the engine: it needs a forked
WireGuard datapath that sing-box has no knowledge of.

The goal is that adding a protocol means **adding one package**, and adding an
engine means **adding one adapter** — with nothing else edited.

## Target shape

```
internal/protocol/              contract + registry. No engine imports. Pure.
internal/protocol/vless/        one package per protocol: parse + render
internal/protocol/vmess/        …
internal/protocol/wireguard/    NEW — config input, endpoint output
internal/protocol/amneziawg/    PHASE 2 — wireguard + obfuscation, own engine
internal/protocol/all/          composition root: builds the default Registry

internal/engine/                engine contract
internal/singbox/               the sing-box engine adapter (schema + assembly)
internal/engine/amneziawg/      PHASE 2
```

Two rules hold the design together:

1. **A protocol module owns its vertical slice.** It parses its own input and
   renders its own engine node. Nothing outside the module knows the shape of
   its parameters — that is what makes `Params any` safe rather than sloppy.
2. **No `init()` registration.** The registry is assembled explicitly in one
   composition root and injected. Registration side effects would make the set
   of supported protocols depend on which packages happened to be linked, which
   is precisely the bug class `with_quic` already cost us once.

## The contract (`internal/protocol`)

```go
type Name string          // "vless", "wireguard", …
type Kind uint8           // KindOutbound | KindEndpoint
type Input uint8          // InputLink (share URL) | InputConfig (INI/blob)

type Descriptor struct {
    Name    Name
    Kind    Kind
    Input   Input
    Schemes []string      // URL schemes claimed; empty for InputConfig
    Title   string        // display name, e.g. "WireGuard"
}

// Profile is engine-agnostic. Params is opaque payload owned by the module
// that produced it; only that module may type-assert it.
type Profile struct {
    Protocol Name
    Label    string       // human name (fragment, vmess "ps", wg file name)
    Raw      string       // original input, for display and re-parse
    Params   any
}

type Module interface {
    Descriptor() Descriptor
    Parse(raw string) (Profile, error)
}

// Relabeler is optional: a module implements it when its input format can carry
// a new display name (every link protocol can; a wg config gets it from the
// user, not the file).
type Relabeler interface {
    SetLabel(raw, label string) (string, error)
}

type Registry struct{ … }
func NewRegistry(modules ...Module) (*Registry, error)   // errors on duplicates
func (r *Registry) Parse(raw string) (Profile, error)
func (r *Registry) Module(Name) (Module, bool)
func (r *Registry) Modules() []Module
func (r *Registry) Schemes() []string                     // for help/error text
```

`Registry.Parse` dispatches on the URL scheme for `InputLink` modules; input
that is not a URL is offered to `InputConfig` modules in registration order,
each of which sniffs (a WireGuard config is recognised by its `[Interface]`
section). An input nothing claims produces one error listing every accepted
scheme — the current behaviour, preserved.

## The engine seam

sing-box renders through a capability interface, so a protocol package pulls in
the sing-box schema only if it actually targets that engine:

```go
// internal/singbox
type Renderer interface {
    // RenderNode returns the config node for this profile: an outbound struct
    // for KindOutbound, an endpoint struct for KindEndpoint.
    RenderNode(p protocol.Profile, o RenderOpts) (any, error)
}

type RenderOpts struct {
    Tag            string
    BindInterface  string   // VPN mode only
    ConnectTimeout string
}
```

The assembly half of `internal/singbox` (config skeleton, DNS bootstrap,
routing rules, urltest group, tunnel/forwarder variants) stays where it is and
becomes engine-adapter code. It asks the registry for each profile's module,
type-asserts `Renderer`, and places the result in `outbounds` or `endpoints`
according to `Descriptor().Kind`. **`endpoints` is a sibling array of
`outbounds` at the top level, and an endpoint's tag is usable anywhere an
outbound tag is** (`adapter.Endpoint` embeds `adapter.Outbound`), so a WireGuard
endpoint joins the urltest failover group like any other server.

```go
// internal/engine
type Engine interface {
    ID() string
    Supports(protocol.Name) bool
    ProxyConfig(set []protocol.Profile, opts ProxyOpts) ([]byte, error)
    ForwarderConfig(set []protocol.Profile, opts ForwarderOpts) ([]byte, error)
    NewCore(ctx context.Context, label string, cfg []byte) (core.Core, error)
}
```

`runtime.Manager` already takes a config builder plus a `core.Factory`; an
Engine is exactly that pair, so the Manager changes shape only slightly.

## Phase 1 — refactor + WireGuard

1. Add `internal/protocol` (contract + registry) and its tests.
2. Move each of the eight existing protocols into `internal/protocol/<name>`,
   carrying its parser, its params struct, its renderer and its tests. Delete
   `internal/link` when the last one moves; no facade is left behind.
3. `internal/singbox` renders through the registry instead of its `switch`.
4. Add `internal/protocol/wireguard`:
   - `Input: InputConfig`, `Kind: KindEndpoint`.
   - Parses the standard INI config (`[Interface]`: `PrivateKey`, `Address`,
     `MTU`, `DNS`; `[Peer]`: `PublicKey`, `PresharedKey`, `Endpoint`,
     `AllowedIPs`, `PersistentKeepalive`). Multiple `[Peer]` sections allowed.
   - Renders a sing-box `wireguard` endpoint: `type`, `tag`, `address[]`,
     `private_key`, `mtu`, `peers[]` with `address`, `port`, `public_key`,
     `pre_shared_key`, `allowed_ips[]`, `persistent_keepalive_interval`,
     `reserved[]`.
   - **Build tags:** sing-box gates WireGuard behind `with_wireguard`, and its
     device is built on a userspace gVisor netstack, so `with_gvisor` is
     required too. Both go into `SINGBOX_TAGS`, `LIBBOX_TAGS` and the release
     script's tag assertion. Without them the endpoint parses and then fails to
     construct — the same silent-dead-key failure `with_quic` produced.

     Enabling gVisor required un-pinning a stale `github.com/sagernet/gvisor`
     in `go.mod`: the fork's version string sorts as NEWER under semver than
     the revision `sing-box`/`sing-tun` actually require (a numeric prerelease
     identifier ranks below an alphanumeric one), so MVS had been silently
     selecting an incompatible older one. This is why the tag appeared not to
     build, and why the repo's build notes claimed gVisor was unusable.
5. Key input gains a second mode (paste config / import file) — the ONLY place a
   picker is justified, because a WireGuard config carries no scheme to dispatch
   on. Everything link-based keeps auto-detection.

## Phase 2 — AmneziaWG

Deliberately separate, because it is not a protocol module on an existing
engine: AmneziaWG changes the WireGuard handshake itself (junk packets
`Jc`/`Jmin`/`Jmax`, header types `H1`–`H4`, init padding `S1`/`S2`). It needs a
forked datapath (`amneziawg-go`) behind an `internal/engine/amneziawg` adapter,
a new third-party dependency, and its own support burden across sing-box
upgrades. It reuses phase 1's config-input plumbing and the engine seam, which
is why it comes second.

## Non-negotiable acceptance criteria

- Every existing test passes **unchanged**, including the golden configs: this
  refactor must not alter a single byte of generated JSON for the eight
  protocols already shipping. Golden files are the regression net.
- `make test-singbox-decode` passes: the real core accepts and starts every
  protocol's generated config, WireGuard included.
- The XHTTP end-to-end suite against real Xray still passes.
- `go build ./...` (stub core) still links without sing-box: the engine adapter
  stays behind the `singbox` build tag, as `internal/core/real.go` does today.
- The conformance suite in `internal/protocol` passes for every registered
  module, so a ninth protocol is covered the moment it is registered.
