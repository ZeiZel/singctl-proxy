# Packaging & release

Installers for the CLI + desktop GUI + boot-start daemon. Outputs go to `dist/`.
Releases are built by `.github/workflows/release.yml` on a `vX.Y.Z` tag; the
commands below reproduce it locally.

## Linux (.deb / .rpm / AppImage)

Built with [nfpm](https://nfpm.goreleaser.com) (no `dpkg`/`rpmbuild` needed) and
linuxdeploy (AppImage). The packages install:

- `singctl` → `/usr/bin/singctl`, `singctl-gui` → `/usr/bin/singctl-gui`
- `singctld@.service` (systemd **template**) → `/usr/lib/systemd/system/`
- man page, `.desktop` launcher, icon
- runtime dep on `libwebkit2gtk-4.1-0` (deb) / `webkit2gtk4.1` (rpm)

```sh
make build-linux                 # CLI amd64+arm64 (pure Go, cross-compiles)
make gui-linux-bin GUI_TAGS=     # GUI (host arch), licensed; needs LICENSE_PUBKEY for real validation
make pkg-linux PKG_ARCH=amd64 PKG_VERSION=1.2.3
make appimage  PKG_ARCH=amd64 PKG_VERSION=1.2.3
```

**Boot-start is per-user** (the root daemon must use a specific user's
`~/.config/singctl`, matching the GUI). The package does not auto-enable it;
after install:

```sh
sudo singctl                          # save a key once (Ключи), then quit
sudo systemctl enable --now singctld@$USER
```

## macOS (.pkg / .dmg, signed + notarized)

The `.pkg` installs the GUI to `/Applications`, the CLI to `/usr/local/bin`, and
a boot-start **LaunchDaemon** (postinstall pins it to the console user). The
`.dmg` is a drag-install of the app. Developer-ID distribution, **app sandbox
off** (like clash-verge-rev).

```sh
make build                       # CLI (real sing-box core, CGO)
make gui GUI_TAGS=               # GUI .app, licensed (set LICENSE_PUBKEY)
make pkg-macos PKG_VERSION=1.2.3 CLI_BIN=bin/singctl-darwin-arm64
```

Signing/notarization apply only when these env vars are set (otherwise unsigned
artifacts are produced for dry runs):

| env | meaning |
| --- | --- |
| `CODESIGN_IDENTITY` | `Developer ID Application: … (TEAMID)` — signs the `.app` |
| `INSTALLER_IDENTITY` | `Developer ID Installer: … (TEAMID)` — signs the `.pkg` |
| `NOTARY_PROFILE` | `notarytool` keychain profile (or the `AC_*` trio below) |
| `AC_APPLE_ID` / `AC_PASSWORD` / `AC_TEAM_ID` | notarization credentials |

## CI release (tag `vX.Y.Z`)

`release.yml` builds the Linux packages (ubuntu) and the macOS installers
(macos), then publishes a GitHub Release with `SHA256SUMS`. Required repo
secrets:

- `LICENSE_PUBKEY` — base64 Ed25519 public key (`bin/singctl-server keygen`),
  embedded so released builds verify licenses.
- macOS (optional; unset → unsigned artifacts): `APPLE_CERT_P12`,
  `APPLE_CERT_PASSWORD`, `CODESIGN_IDENTITY`, `INSTALLER_IDENTITY`,
  `AC_APPLE_ID`, `AC_PASSWORD`, `AC_TEAM_ID`.

> Windows installers and Linux **arm64** GUI packages are not built yet (the GUI
> needs a native runner per arch); the cross-compiled CLI binaries ship for all
> targets regardless.
