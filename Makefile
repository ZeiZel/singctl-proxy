GO ?= go
BINARY := singctl
PKG := ./...
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# LICENSE_PUBKEY (base64 Ed25519 public key from `bin/singctl-server keygen`) is
# embedded into the CLI so it can verify licenses offline. Release builds MUST
# set it; dev builds without it cannot verify any license (use build-unlicensed
# for local runs). Never embed the PRIVATE key — it lives only on the server.
LICENSE_PUBKEY ?=
ifneq ($(LICENSE_PUBKEY),)
LDFLAGS += -X singctl/internal/license.PublicKeyB64=$(LICENSE_PUBKEY)
endif

# LICENSE_SERVER_URL is the baked-in default license/revocation server base URL
# (SINGCTL_LICENSE_SERVER still overrides it at runtime without a rebuild).
# Release builds should set it so a normal install activates/re-checks without
# any extra configuration.
LICENSE_SERVER_URL ?=
ifneq ($(LICENSE_SERVER_URL),)
LDFLAGS += -X singctl/internal/license.LicenseServerDefault=$(LICENSE_SERVER_URL)
endif

# man page install location: `make install-man` enables `man singctl`.
MANPREFIX ?= /usr/local/share/man
MANPAGE := cmd/singctl/singctl.1

# sing-box build tags. `singbox` links the real core; `with_utls` is REQUIRED for
# REALITY/uTLS; `with_clash_api` is REQUIRED because singctl enables sing-box's
# Clash API by default (connection logging + per-server latency) — without it the
# core fails at startup with "clash api is not included in this build". We use the
# TUN system stack, so `with_gvisor` is intentionally omitted (it also fails to
# build with sing-tun's pinned gVisor version).
SINGBOX_TAGS := singbox with_utls with_clash_api

.PHONY: build build-macos build-windows build-all build-netext app-macos \
	build-unlicensed build-server docker-server \
	test test-integration tidy run lint clean install-man uninstall-man \
	install uninstall gui gui-dev gui-test gui-reset \
	pkg-macos

# Install prefix for the binary (`make install`).
PREFIX ?= /usr/local
UNAME_S := $(shell uname -s)

# Shipping build: links the real sing-box core (-tags singbox). Requires the
# library in the module graph first: `go get github.com/sagernet/sing-box@v1.12.x`.
# CGO is required for the sing-box TUN on darwin.
build:
	CGO_ENABLED=1 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/singctl

# Build with license enforcement COMPILED OUT (-tags unlicensed). For local
# development only — the bypass code is not present in any other build, and this
# target must never be published (CI release builds omit the tag). GNU make can't
# use a literal ':' in a target name, so this is `build-unlicensed` (not
# `build:unlicensed`).
build-unlicensed:
	CGO_ENABLED=1 $(GO) build -tags "$(SINGBOX_TAGS) unlicensed" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-unlicensed ./cmd/singctl
	@echo "Built bin/$(BINARY)-unlicensed — license checks DISABLED (dev only)."

# License server: pure Go (no sing-box, CGO-free), embeds bbolt. Container image
# is built from deploy/server.Dockerfile.
build-server:
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-server ./cmd/server

docker-server:
	docker build -f deploy/server.Dockerfile -t singctl-license:$(VERSION) .

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

# Build the macOS transparent-proxy System Extension scaffold (Variant C of the
# Cursor-leak plan, bead singctl-proxy-4uy). macOS-only: needs Xcode + XcodeGen
# (`brew install xcodegen`) and an Apple Developer Team ID. The extension catches
# an app's WHOLE network stack (Chromium, Node/undici, raw sockets) — the only
# way to fully proxy Cursor/VS Code per-app on macOS. After building, run the
# .app once to approve the extension, then notarize (see the script's output).
#
#   make build-netext                          # uses the default team below
#   make build-netext DEVELOPMENT_TEAM=<your-team-id>   # override for another account
#
# DEVELOPMENT_TEAM defaults to the project's Apple Developer Team ID; override it
# on the command line to build/sign with a different account.
DEVELOPMENT_TEAM ?= S3UCF4USYC
NETEXT_DIR := packaging/macos/netextension
build-netext:
ifeq ($(UNAME_S),Darwin)
	DEVELOPMENT_TEAM="$(DEVELOPMENT_TEAM)" $(NETEXT_DIR)/build.sh
else
	@echo "build-netext is macOS-only (needs Xcode + the NetworkExtension SDK)." >&2; exit 1
endif

# Native SwiftUI macOS app (macos/Singctl/) that HOSTS the embedded
# ProxyExtension transparent-proxy system extension — replaces the Wails
# `gui` target in the packaging pipeline (build-netext/netextension's
# separate SingctlProxy.app container is now folded into this app). Needs
# Xcode + XcodeGen (`brew install xcodegen`) and an Apple Developer Team ID;
# see macos/Singctl/build.sh for the full env knobs. Signing identity +
# provisioning profiles are pinned in macos/Singctl/project.yml (Developer
# ID, manual signing) so the output is already signed — no separate
# codesign step needed afterward.
#
#   make app-macos                                 # uses the default team below
#   make app-macos DEVELOPMENT_TEAM=<your-team-id>  # override for another account
#   make app-macos NOTARY_PROFILE=<profile>         # also notarize+staple the .app
#
# Output: macos/Singctl/build/Build/Products/Release/Singctl.app
NOTARY_PROFILE ?=
app-macos:
ifeq ($(UNAME_S),Darwin)
	DEVELOPMENT_TEAM="$(DEVELOPMENT_TEAM)" NOTARY_PROFILE="$(NOTARY_PROFILE)" macos/Singctl/build.sh
else
	@echo "app-macos is macOS-only (needs Xcode + XcodeGen)." >&2; exit 1
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
# desktop GUI drives over the control socket. SUDO is empty when already root
# so `sudo make install` also works. macOS-only.
SUDO := $(shell [ "$$(id -u)" = "0" ] || echo sudo)
install: build
	./scripts/install-macos.sh

uninstall:
	./scripts/install-macos.sh uninstall

# --- Desktop GUI (Wails: Go + React, drives the daemon over the control socket) ---
# SUPERSEDED for packaging by `app-macos` (macos/Singctl/, native SwiftUI) — kept
# only until a later phase removes gui/ entirely. `make pkg-macos` no longer
# builds or stages this target.
# The GUI lives in its own nested module (gui/) so it never pulls sing-box into
# the main build. No platform build tag is needed on macOS. WAILS resolves to
# an installed `wails` (on PATH or in $(go env GOPATH)/bin), else falls back to
# `go run` so no global install is required. Override with `make gui WAILS=wails`
# once it is on your PATH.
WAILS_VERSION ?= v2.12.0
WAILS ?= $(shell command -v wails 2>/dev/null || ([ -x "$$(go env GOPATH)/bin/wails" ] && echo "$$(go env GOPATH)/bin/wails") || echo "go run github.com/wailsapp/wails/v2/cmd/wails@$(WAILS_VERSION)")
# Dev GUI builds compile out the license gate (like build-unlicensed): the GUI's
# License screen then shows "development build". Production packaging overrides
# `GUI_TAGS=` and embeds the pubkey (LICENSE_PUBKEY) to enable real validation.
GUI_TAGS ?= unlicensed
GUI_ALL_TAGS := $(strip $(GUI_TAGS))
GUI_TAGFLAG := $(if $(GUI_ALL_TAGS),-tags "$(GUI_ALL_TAGS)",)
GUI_LDX := $(if $(strip $(LICENSE_PUBKEY)),-X singctl/internal/license.PublicKeyB64=$(LICENSE_PUBKEY),)
GUI_LDX += $(if $(strip $(LICENSE_SERVER_URL)),-X singctl/internal/license.LicenseServerDefault=$(LICENSE_SERVER_URL),)
GUI_LDFLAGS := $(if $(strip $(GUI_LDX)),-ldflags "$(strip $(GUI_LDX))",)
gui:
	cd gui && $(WAILS) build $(GUI_TAGFLAG) $(GUI_LDFLAGS)

gui-dev:
	cd gui && $(WAILS) dev $(GUI_TAGFLAG) $(GUI_LDFLAGS)

gui-test:
	cd gui && go test ./...
	cd gui/frontend && npm run test

# Wipe the frontend's installed deps + lockfile. Needed when switching the OS that
# builds the GUI on a shared checkout (node_modules holds platform-specific
# rollup/esbuild binaries); the next `make gui`/`gui-dev` reinstalls them.
gui-reset:
	rm -rf gui/frontend/node_modules gui/frontend/package-lock.json gui/frontend/dist

# --- Installers / packaging (output to dist/) ---
# Version without a leading 'v' (deb/rpm reject it); release tags are clean.
PKG_VERSION ?= $(patsubst v%,%,$(VERSION))
PKG_ARCH ?= $(shell $(GO) env GOARCH)

# macOS notarized .pkg + .dmg. Needs the GUI (make gui) + CLI (make build) built;
# signing/notarization apply only when the identity/cred env vars are set.
pkg-macos:
	PKG_VERSION="$(PKG_VERSION)" packaging/macos/build-installers.sh

clean:
	rm -rf bin dist gui/build/bin gui/frontend/dist
