GO ?= go
BINARY := singctl
PKG := ./...
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# man page install location: `make install-man` enables `man singctl`.
MANPREFIX ?= /usr/local/share/man
MANPAGE := cmd/singctl/singctl.1

# sing-box build tags. `singbox` links the real core; `with_utls` is REQUIRED for
# REALITY/uTLS; `with_clash_api` is REQUIRED because singctl enables sing-box's
# Clash API by default (connection logging + per-server latency) — without it the
# core fails at startup with "clash api is not included in this build". We use the
# TUN system stack, so `with_gvisor` is intentionally omitted (it also fails to
# build with sing-tun's pinned gVisor version).
# with_gvisor is required by with_wireguard: sing-box builds its WireGuard
# device on a userspace gVisor netstack, and without the tag the endpoint
# decodes and then fails with "gVisor is not included in this build". It does
# NOT change our own TUN, which pins stack:"system". Enabling it required
# un-pinning a stale github.com/sagernet/gvisor in go.mod — the fork's version
# string sorts as newer than the one sing-box/sing-tun actually need, so MVS
# had been silently selecting an incompatible older revision.
#
# with_utls is required for REALITY; with_quic gates the QUIC-based protocols
# (hysteria, hysteria2, tuic) — without it sing-box parses their outbounds and
# then refuses to construct them ("QUIC is not included in this build").
# with_wireguard gates the wireguard endpoint the same way: without it, a
# WireGuard config decodes fine and then fails to construct at runtime — the
# same silent-dead-key failure a missing with_quic already cost us once.
SINGBOX_TAGS := singbox with_utls with_clash_api with_quic with_wireguard with_gvisor

.PHONY: build build-macos build-windows build-all app-macos appstore libbox \
	test test-integration tidy run lint clean install-man uninstall-man \
	install uninstall \
	pkg-macos \
	proxy-on proxy-off proxy-status proxy-pac pac-server

# Install prefix for the binary (`make install`).
PREFIX ?= /usr/local
UNAME_S := $(shell uname -s)

# Shipping build: links the real sing-box core (-tags singbox). Requires the
# library in the module graph first: `go get github.com/sagernet/sing-box@v1.12.x`.
# CGO is required for the sing-box TUN on darwin.
build:
	CGO_ENABLED=1 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/singctl

# --- cross-platform builds (bin/<binary>-<os>-<arch>) ---
#
# macOS needs CGO for the sing-box TUN (both arches build natively on a Mac:
# clang cross-assembles arm64<->amd64). Windows builds are pure Go
# (CGO_ENABLED=0): sing-tun uses wintun on Windows, so it cross-compiles from
# any host. Windows 10/11 = windows/amd64 (+arm64). Run the Windows binary as
# Administrator and put wintun.dll (https://www.wintun.net) next to it for VPN
# mode.

build-macos:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 ./cmd/singctl
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64 ./cmd/singctl

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-amd64.exe ./cmd/singctl
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-arm64.exe ./cmd/singctl

build-all: build-macos build-windows

# Native SwiftUI macOS app (macos/Singctl/) that HOSTS the embedded
# ProxyExtension transparent-proxy system extension (captures an app's WHOLE
# network stack — Chromium, Node/undici, raw sockets — the only way to fully
# proxy Cursor/VS Code per-app on macOS). Needs Xcode + XcodeGen (`brew install
# xcodegen`) and an Apple Developer Team ID; see macos/Singctl/build.sh for the
# full env knobs. Signing identity + provisioning profiles are pinned in
# macos/Singctl/project.yml (Developer ID, manual signing) so the output is
# already signed — no separate codesign step needed afterward. After building,
# run the .app once to approve the extension, then notarize (see the script's
# output).
#
#   make app-macos                                 # uses the default team below
#   make app-macos DEVELOPMENT_TEAM=<your-team-id>  # override for another account
#   make app-macos NOTARY_PROFILE=<profile>         # also notarize+staple the .app
#
# Output: macos/Singctl/build/Build/Products/Release/Singctl.app
DEVELOPMENT_TEAM ?= S3UCF4USYC
NOTARY_PROFILE ?=
app-macos:
ifeq ($(UNAME_S),Darwin)
	DEVELOPMENT_TEAM="$(DEVELOPMENT_TEAM)" NOTARY_PROFILE="$(NOTARY_PROFILE)" macos/Singctl/build.sh
else
	@echo "app-macos is macOS-only (needs Xcode + XcodeGen)." >&2; exit 1
endif

# App Store SKU (flagged SingctlAppStore target in macos/Singctl/project.yml,
# SWIFT_ACTIVE_COMPILATION_CONDITIONS=APPSTORE): a sandboxed container app +
# NEPacketTunnelProvider appex (PacketTunnel), built from the SAME
# macos/Singctl/App/ sources as `make app-macos` rather than a separate
# project. See docs/appstore-sku.md for the full architecture/runbook.
#
# NOTE: this archives and exports today, but the PacketTunnel appex is still a
# stub (macos/Singctl/PacketTunnel/PacketTunnelProvider.swift) — a real VPN
# datapath needs `gomobile bind` to produce Libbox.xcframework (the sing-box
# core wrapped for Swift) and wiring it into the appex's startTunnel, which is
# NOT done by this target yet.
#
#   make appstore                                 # uses the default team below
#   make appstore DEVELOPMENT_TEAM=<your-team-id>  # override for another account
#
# Output: dist/SingctlAppStore.ipa (App Store Connect upload package) — see
# macos/Singctl/ExportOptions-appstore.plist for the export method/team.
appstore:
ifeq ($(UNAME_S),Darwin)
	cd macos/Singctl && xcodegen generate
	cd macos/Singctl && xcodebuild \
		-scheme SingctlAppStore \
		-configuration Release \
		-derivedDataPath ./build \
		-archivePath build/SingctlAppStore.xcarchive \
		-allowProvisioningUpdates \
		DEVELOPMENT_TEAM="$(DEVELOPMENT_TEAM)" \
		archive
	mkdir -p dist
	cd macos/Singctl && xcodebuild -exportArchive \
		-archivePath build/SingctlAppStore.xcarchive \
		-exportPath ../../dist \
		-exportOptionsPlist ExportOptions-appstore.plist \
		-allowProvisioningUpdates
else
	@echo "appstore is macOS-only (needs Xcode + XcodeGen)." >&2; exit 1
endif

# gomobile-built sing-box core for the App Store SKU. Binds sing-box's
# experimental/libbox PLUS the repo-local ./mobile shim (mobile.BuildConfig,
# which turns the container app's TunnelConfig JSON into a sing-box config via
# internal/vless + internal/singbox) into macos/Singctl/Libbox.xcframework —
# the framework PacketTunnel/ imports. ~68MB, gitignored; rerun after bumping
# sing-box or changing ./mobile. Uses sagernet's gomobile fork (pinned in
# go.mod) and the same minimal build tags as the daemon (SINGBOX_TAGS).
LIBBOX_TAGS := with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor,badlinkname,tfogo_checklinkname0,grpcnotrace
libbox:
ifeq ($(UNAME_S),Darwin)
	$(GO) install github.com/sagernet/gomobile/cmd/gomobile github.com/sagernet/gomobile/cmd/gobind
	PATH="$(shell $(GO) env GOPATH)/bin:$$PATH" gomobile bind -v -target macos -libname=box \
		-trimpath -buildvcs=false \
		-ldflags "-X github.com/sagernet/sing-box/constant.Version=$(VERSION) -s -w -buildid= -checklinkname=0" \
		-tags "$(LIBBOX_TAGS)" \
		-o macos/Singctl/Libbox.xcframework \
		github.com/sagernet/sing-box/experimental/libbox ./mobile
else
	@echo "libbox is macOS-only (needs the macOS/gomobile toolchain)." >&2; exit 1
endif

# Hermetic dev build: stub core, no sing-box dependency. Used by the unit suite.
build-stub:
	$(GO) build ./...

# Hermetic unit suite: no root, no real sing-box tunnel, no Cisco interaction.
test:
	$(GO) test $(PKG)

# Schema integration: verifies generated configs decode in the REAL sing-box.
# Needs the library first: go get github.com/sagernet/sing-box@v1.12.x
test-singbox-decode:
	$(GO) test -tags "integration $(SINGBOX_TAGS)" ./internal/core/

# XHTTP against the reference implementation: starts a real Xray-core server
# locally and runs the whole stack through it (packet-up/stream-up/stream-one
# over HTTP/1.1 and HTTP/2, REALITY, and a urltest group). Skipped without
# XRAY_BIN; grab a binary from https://github.com/XTLS/Xray-core/releases.
#
#   make test-xhttp-e2e XRAY_BIN=/path/to/xray
XRAY_BIN ?=
test-xhttp-e2e:
	XRAY_BIN="$(XRAY_BIN)" $(GO) test -v -tags "integration $(SINGBOX_TAGS)" -run TestXHTTP_EndToEnd ./internal/core/

# Live integration suite: root + a live machine (see PLAN.md §8 checklist).
test-integration:
	sudo $(GO) test -tags "integration $(SINGBOX_TAGS)" $(PKG)

tidy:
	$(GO) mod tidy

# VPN/TUN mode needs root; the whole utility runs under sudo (decision D2/§0).
run: build
	sudo ./bin/$(BINARY)

lint:
	$(GO) vet $(PKG)

# Install the man page so `man singctl` works (may need sudo).
install-man:
	install -d $(MANPREFIX)/man1
	install -m 0644 $(MANPAGE) $(MANPREFIX)/man1/singctl.1

uninstall-man:
	rm -f $(MANPREFIX)/man1/singctl.1

# Install singctl as a system tool. Run plain `make install` (NOT under sudo) so
# the build stays non-root; the install step self-elevates with sudo for the
# privileged copy. Installs a LaunchDaemon (scripts/install-macos.sh) that runs
# a boot-start, system-wide VPN daemon (singctl --headless --vpn) that the
# desktop app drives over the control socket. SUDO is empty when already root
# so `sudo make install` also works. macOS-only.
SUDO := $(shell [ "$$(id -u)" = "0" ] || echo sudo)
install: build
	./scripts/install-macos.sh

uninstall:
	./scripts/install-macos.sh uninstall

# --- Installers / packaging (output to dist/) ---
# Version without a leading 'v' (deb/rpm reject it); release tags are clean.
PKG_VERSION ?= $(patsubst v%,%,$(VERSION))
PKG_ARCH ?= $(shell $(GO) env GOARCH)

# Full macOS release in one step: stamps the version everywhere, builds the CLI,
# the app + embedded system extension and the signed installers, then VERIFIES
# that every artifact carries the same version (a release once shipped as 1.5.0
# with a 1.4.1 app bundle inside). See README-build.md "Релиз macOS".
#
#   make release-macos RELEASE_VERSION=1.5.0
#   make release-macos RELEASE_VERSION=1.5.0 RELEASE_ARGS=--skip-app
#   make release-macos RELEASE_VERSION=1.5.0 RELEASE_ARGS=--unsigned-installer
RELEASE_VERSION ?=
RELEASE_ARGS ?=
release-macos:
	@[ -n "$(RELEASE_VERSION)" ] || { echo "usage: make release-macos RELEASE_VERSION=1.5.0" >&2; exit 2; }
	./scripts/release-macos.sh $(RELEASE_VERSION) $(RELEASE_ARGS)

# Stamp a version into the app/extension Info.plists without building anything.
#   make set-version RELEASE_VERSION=1.5.0
set-version:
	@[ -n "$(RELEASE_VERSION)" ] || { echo "usage: make set-version RELEASE_VERSION=1.5.0" >&2; exit 2; }
	./scripts/set-version.sh $(RELEASE_VERSION)

# macOS notarized .pkg + .dmg. Needs the app (make app-macos) + CLI (make build)
# built; signing/notarization apply only when the identity/cred env vars are set.
# Prefer `make release-macos` — it also stamps versions and verifies the result.
pkg-macos:
	PKG_VERSION="$(PKG_VERSION)" packaging/macos/build-installers.sh

clean:
	rm -rf bin dist

# --- macOS system proxy toggle -------------------------------------------------
# Two mutually-exclusive modes, both driven by a generated PAC (Automatic Proxy
# Configuration) so they switch cleanly and can express rules the manual proxy
# fields cannot — exclude simple hostnames, large domain lists, IP ranges:
#   proxy-on  = EXCLUDE mode: proxy everything EXCEPT private/Russian +
#               simple hostnames. See gen-exclude-pac.sh for the full rule set.
#   proxy-pac = INCLUDE mode: proxy ONLY the allowlist (proxy-domains.txt).
#   proxy-off = disable both manual proxy and PAC.
# Override the service if not on Wi-Fi: `make proxy-on PROXY_SERVICE=Ethernet`.
PROXY_SERVICE ?= Wi-Fi
PROXY_HOST    ?= 127.0.0.1
PROXY_PORT    ?= 2080

# The PAC is served over http:// by a tiny localhost LaunchAgent, NOT file://,
# because Chrome/Chromium refuses to load file:// PAC scripts (they silently
# fall through to DIRECT). PAC_PORT is where that server listens; the generated
# .pac files live in ~/.config/singctl and are what it serves.
PAC_PORT         ?= 21080
PAC_AGENT_LABEL  := com.singctl.pacserver
PAC_AGENT_PLIST  := $(HOME)/Library/LaunchAgents/$(PAC_AGENT_LABEL).plist
PAC_DIR          := $(HOME)/.config/singctl
PAC_URL_BASE     := http://127.0.0.1:$(PAC_PORT)

# Install/reload the localhost PAC server LaunchAgent (idempotent).
pac-server:
	@mkdir -p "$(PAC_DIR)" "$(HOME)/Library/LaunchAgents"
	@sed -e "s#__CONFIG_DIR__#$(PAC_DIR)#g" -e "s#__PORT__#$(PAC_PORT)#g" \
	    packaging/macos/com.singctl.pacserver.plist.in > "$(PAC_AGENT_PLIST)"
	@launchctl bootout gui/$$(id -u)/$(PAC_AGENT_LABEL) 2>/dev/null || true
	@launchctl bootstrap gui/$$(id -u) "$(PAC_AGENT_PLIST)"
	@sleep 1; curl -sf --noproxy '*' -o /dev/null "$(PAC_URL_BASE)/" \
	  && echo "pac-server up on $(PAC_URL_BASE)" \
	  || echo "pac-server WARN: not reachable yet (check /tmp/singctl-pacserver.log)"

# Ensure the server is up without a full reload when it already answers.
define ensure_pac_server
	curl -sf --noproxy '*' -o /dev/null "$(PAC_URL_BASE)/" 2>/dev/null \
	  || $(MAKE) --no-print-directory pac-server
endef

# EXCLUDE-mode (proxy-on) keeps Russian + simple-hostname traffic off the
# proxy. Private ranges (RFC1918 + CGNAT 100.64/10, link-local) and
# .ru/.xn--p1ai(=.рф) are baked into the PAC directly. The broader set of
# Russian services on non-.ru TLDs comes from a domain list fetched from v2fly
# `category-ru`: RU_CACHE is the live copy, RU_BASELINE the committed offline
# fallback. proxy-on builds the PAC instantly from whichever exists (cache wins)
# and refreshes the cache in the BACKGROUND (the full fetch is ~1min; the new
# list applies on the next proxy-on). FETCH_PROXY routes that refresh through
# Singctl so it works even when the Cisco path can't reach GitHub.
RU_BASELINE      ?= packaging/macos/ru-extra.txt
RU_CACHE         ?= $(HOME)/.config/singctl/ru-domains.txt
EXCLUDE_PAC_PATH ?= $(HOME)/.config/singctl/proxy-exclude.pac

proxy-on:
	@mkdir -p "$(PAC_DIR)"
	@ru="$(RU_CACHE)"; [ -f "$$ru" ] || ru="$(RU_BASELINE)"; \
	  packaging/macos/gen-exclude-pac.sh "$$ru" "PROXY $(PROXY_HOST):$(PROXY_PORT)" > "$(EXCLUDE_PAC_PATH)"; \
	  echo "proxy-on: exclude PAC built from $$ru"
	@$(ensure_pac_server)
	networksetup -setwebproxystate "$(PROXY_SERVICE)" off
	networksetup -setsecurewebproxystate "$(PROXY_SERVICE)" off
	networksetup -setautoproxyurl "$(PROXY_SERVICE)" "$(PAC_URL_BASE)/proxy-exclude.pac"
	networksetup -setautoproxystate "$(PROXY_SERVICE)" on
	@echo "proxy ON (exclude mode) -> $(PAC_URL_BASE)/proxy-exclude.pac on '$(PROXY_SERVICE)'"
	@( FETCH_PROXY="$(PROXY_HOST):$(PROXY_PORT)" packaging/macos/fetch-ru-domains.sh "$(RU_CACHE)" >/dev/null 2>&1 & ) ; \
	  echo "proxy-on: RU domain list refreshing in background -> $(RU_CACHE)"

proxy-off:
	networksetup -setwebproxystate "$(PROXY_SERVICE)" off
	networksetup -setsecurewebproxystate "$(PROXY_SERVICE)" off
	networksetup -setautoproxystate "$(PROXY_SERVICE)" off
	@echo "proxy OFF on '$(PROXY_SERVICE)' (manual + PAC)"

proxy-status:
	@echo "== $(PROXY_SERVICE) web proxy ==";        networksetup -getwebproxy "$(PROXY_SERVICE)"
	@echo "== $(PROXY_SERVICE) secure web proxy =="; networksetup -getsecurewebproxy "$(PROXY_SERVICE)"
	@echo "== bypass domains ==";                    networksetup -getproxybypassdomains "$(PROXY_SERVICE)"
	@echo "== auto proxy (PAC) ==";                  networksetup -getautoproxyurl "$(PROXY_SERVICE)"

# Include-mode: proxy ONLY the domains in $(PROXY_DOMAINS_FILE) (and their
# subdomains); everything else goes DIRECT. Generates a PAC from the list and
# switches the service to Automatic Proxy Configuration, turning the manual
# web/secure proxy off so the two mechanisms don't overlap. Edit the domain list
# and re-run to update. Add domains inline: append to proxy-domains.txt.
PROXY_DOMAINS_FILE ?= packaging/macos/proxy-domains.txt
PROXY_PAC_PATH     ?= $(HOME)/.config/singctl/proxy.pac

proxy-pac:
	@mkdir -p "$(PAC_DIR)"
	packaging/macos/gen-pac.sh "$(PROXY_DOMAINS_FILE)" "PROXY $(PROXY_HOST):$(PROXY_PORT)" > "$(PROXY_PAC_PATH)"
	@$(ensure_pac_server)
	networksetup -setwebproxystate "$(PROXY_SERVICE)" off
	networksetup -setsecurewebproxystate "$(PROXY_SERVICE)" off
	networksetup -setautoproxyurl "$(PROXY_SERVICE)" "$(PAC_URL_BASE)/proxy.pac"
	networksetup -setautoproxystate "$(PROXY_SERVICE)" on
	@echo "proxy PAC (include mode) -> $(PAC_URL_BASE)/proxy.pac on '$(PROXY_SERVICE)'"
	@echo "proxy PAC ON -> file://$(PROXY_PAC_PATH) on '$(PROXY_SERVICE)'"
