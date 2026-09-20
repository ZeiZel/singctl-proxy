// Package mobile is the gomobile-bound shim between the App Store SKU's
// Swift side and the pure Go config packages. See docs/appstore-sku.md.
//
// The container app (macos/Singctl/App/Core/TunnelBackend.swift) persists
// mode/keys/settings as a TunnelConfig struct to config.json in the shared
// App Group container. The PacketTunnel appex (a sandboxed
// NEPacketTunnelProvider) has no way to run internal/protocol + internal/singbox
// directly — those stay pure Go packages with no sing-box import — so this
// package is bound to a Libbox.xcframework via `gomobile bind` and called
// from Swift to turn that JSON into the actual sing-box config JSON the appex
// hands to Libbox.
//
// gomobile can only bind a narrow subset of Go: exported functions/methods
// may take and return only basic types (string, []byte, numeric, bool),
// error, and a restricted set of struct/interface shapes — no arbitrary Go
// structs, generics, or embedding. We deliberately keep the exported surface
// down to plain strings and error so the binding stays mechanical.
package mobile

import (
	"encoding/json"
	"fmt"

	"singctl/internal/protocol/all"
	"singctl/internal/singbox"
)

// tunnelConfig mirrors TunnelConfig in
// macos/Singctl/App/Core/TunnelBackend.swift. That struct has no CodingKeys
// override, so Swift's JSONEncoder emits its property names verbatim:
// "mode", "keys", "settings".
type tunnelConfig struct {
	Mode     string         `json:"mode"`
	Keys     []string       `json:"keys"`
	Settings tunnelSettings `json:"settings"`
}

// tunnelSettings mirrors the wire shape of Settings in
// macos/Singctl/App/Core/Models.swift, whose CodingKeys map every field to a
// PascalCase wire name (matching the Developer-ID daemon's control-socket
// protocol, which Settings was originally written for). We only read the
// three urltest fields: SocksPort/ClashEnabled/ClashAddr/SaveProfile describe
// a local proxy listener and a config-save toggle that don't exist in the
// sandboxed single-tun-instance model, so they are decoded (to keep
// json.Unmarshal from erroring on unknown-vs-present fields — it wouldn't
// either way) but otherwise ignored.
type tunnelSettings struct {
	URLTestURL       string `json:"URLTestURL"`
	URLTestInterval  string `json:"URLTestInterval"`
	URLTestTolerance int    `json:"URLTestTolerance"`
}

// coreVersion is the sing-box core version vendored via go.mod
// (github.com/sagernet/sing-box). sing-box's own constant.Version is a build-
// time ldflags var (defaults to "unknown" without them, since gomobile bind
// doesn't run our Makefile's ldflags step), so hardcoding the pinned go.mod
// version is more useful than importing it — bump this alongside go.mod when
// the dependency is upgraded.
const coreVersion = "sing-box 1.13.12"

// BuildConfig turns configJSON — the App Group's config.json contents,
// written by TunnelConfigStore.save in TunnelBackend.swift
// ({"mode":...,"keys":[...],"settings":{...}}) — into the sing-box config
// JSON for the sandboxed single-tun-instance VPN (GenerateTunnelConfigSet),
// ready for the appex to hand to Libbox.
//
// Errors are returned, never panicked: they surface directly in the appex's
// os_log and (via the container app reading NEVPNStatus/whatever error
// channel wraps this call) potentially to the end user, so every error here
// is wrapped with enough context (which stage failed, and why) to diagnose a
// bad key or a malformed settings blob without attaching a debugger to a
// sandboxed process.
func BuildConfig(configJSON string) (string, error) {
	var tc tunnelConfig
	if err := json.Unmarshal([]byte(configJSON), &tc); err != nil {
		return "", fmt.Errorf("mobile: parse tunnel config JSON: %w", err)
	}
	if len(tc.Keys) == 0 {
		return "", fmt.Errorf("mobile: no keys configured")
	}

	reg := all.Registry()
	profiles, err := reg.ParseAll(tc.Keys)
	if err != nil {
		return "", fmt.Errorf("mobile: parse keys: %w", err)
	}

	opts := singbox.TunnelOpts{
		URLTest: singbox.URLTestParams{
			URL:       tc.Settings.URLTestURL,
			Interval:  tc.Settings.URLTestInterval,
			Tolerance: tc.Settings.URLTestTolerance,
		},
	}

	cfg, err := singbox.GenerateTunnelConfigSet(reg, profiles, opts)
	if err != nil {
		return "", fmt.Errorf("mobile: generate tunnel config: %w", err)
	}

	out, err := singbox.MarshalIndented(cfg)
	if err != nil {
		return "", fmt.Errorf("mobile: marshal tunnel config: %w", err)
	}
	return string(out), nil
}

// Version reports the embedded sing-box core version, surfaced by the
// container app in diagnostics / support requests.
func Version() string {
	return coreVersion
}
