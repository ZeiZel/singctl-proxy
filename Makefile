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

.PHONY: build build-macos build-windows build-linux build-all build-netext \
	build-unlicensed build-server docker-server \
	test test-integration tidy run lint clean install-man uninstall-man \
	install uninstall gui gui-dev gui-test

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
# clang cross-assembles arm64<->amd64). Linux and Windows builds are pure Go
# (CGO_ENABLED=0): sing-tun uses netlink on Linux and wintun on Windows, so
# they cross-compile from any host. Windows 10/11 = windows/amd64 (+arm64);
# Ubuntu = linux/amd64 (+arm64). Run the Windows binary as Administrator and
# put wintun.dll (https://www.wintun.net) next to it for VPN mode.

build-macos:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 ./cmd/singctl
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64 ./cmd/singctl

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 ./cmd/singctl
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 ./cmd/singctl

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-amd64.exe ./cmd/singctl
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -tags "$(SINGBOX_TAGS)" -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-arm64.exe ./cmd/singctl

build-all: build-macos build-linux build-windows

# Build the macOS transparent-proxy System Extension scaffold (Variant C of the
# Cursor-leak plan, bead singctl-proxy-4uy). macOS-only: needs Xcode + XcodeGen
# (`brew install xcodegen`) and an Apple Developer Team ID. The extension catches
# an app's WHOLE network stack (Chromium, Node/undici, raw sockets) — the only
# way to fully proxy Cursor/VS Code per-app on macOS. After building, run the
# .app once to approve the extension, then notarize (see the script's output).
#
#   make build-netext DEVELOPMENT_TEAM=<your-team-id>
#
NETEXT_DIR := packaging/macos/netextension
build-netext:
ifeq ($(UNAME_S),Darwin)
	DEVELOPMENT_TEAM="$(DEVELOPMENT_TEAM)" $(NETEXT_DIR)/build.sh
else
	@echo "build-netext is macOS-only (needs Xcode + the NetworkExtension SDK)." >&2; exit 1
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
# privileged copy. On macOS this installs a LaunchDaemon (scripts/install-macos.sh);
# on Linux a systemd unit (scripts/install-linux.sh). Both run a boot-start,
# system-wide VPN daemon (singctl --headless --vpn) that the desktop GUI drives
# over the control socket. SUDO is empty when already root so `sudo make install`
# also works.
SUDO := $(shell [ "$$(id -u)" = "0" ] || echo sudo)
install: build
ifeq ($(UNAME_S),Darwin)
	./scripts/install-macos.sh
else
	PREFIX=$(PREFIX) ./scripts/install-linux.sh
endif

uninstall:
ifeq ($(UNAME_S),Darwin)
	./scripts/install-macos.sh uninstall
else
	PREFIX=$(PREFIX) ./scripts/install-linux.sh uninstall
endif

# --- Desktop GUI (Wails: Go + React, drives the daemon over the control socket) ---
# The GUI lives in its own nested module (gui/) so it never pulls sing-box into
# the main build. On Linux it needs the webkit2_41 build tag (Ubuntu ships
# webkit2gtk-4.1, not 4.0 — `wails doctor` falsely reports it missing). Requires
# the wails CLI on PATH (go install github.com/wailsapp/wails/v2/cmd/wails@latest).
WAILS_TAGS ?= webkit2_41
gui:
	cd gui && wails build -tags $(WAILS_TAGS)

gui-dev:
	cd gui && wails dev -tags $(WAILS_TAGS)

gui-test:
	cd gui && go test ./...
	cd gui/frontend && npm run test

clean:
	rm -rf bin gui/build/bin gui/frontend/dist
