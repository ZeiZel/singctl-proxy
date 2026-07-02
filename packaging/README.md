# Packaging & release

Installers for the CLI + desktop GUI + boot-start daemon. Outputs go to `dist/`.
Releases are built by the `release:linux`/`release:macos`/`release:publish`
jobs in `.gitlab-ci.yml` on a `vX.Y.Z` tag (previously
`.github/workflows/release.yml`); the commands below reproduce it locally.

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
| `CODESIGN_IDENTITY` | `Developer ID Application: <Name> (S3UCF4USYC)` — signs the `.app` |
| `INSTALLER_IDENTITY` | `Developer ID Installer: <Name> (S3UCF4USYC)` — signs the `.pkg` |
| `NOTARY_PROFILE` | `notarytool` keychain profile (or the `AC_*` trio below) |
| `AC_APPLE_ID` / `AC_PASSWORD` / `AC_TEAM_ID` | notarization credentials, e.g. `AC_TEAM_ID=S3UCF4USYC` |

## CI release (tag `vX.Y.Z`)

`.gitlab-ci.yml` builds the release artifacts in three jobs:

- `release:linux` — runs automatically on the shared `golang:1.24-bookworm`
  image, produces the `.deb`/`.rpm`/`.AppImage`.
- `release:macos` — a **manual** job (`when: manual`, `allow_failure: true`)
  that only runs on a **self-hosted** GitLab Runner tagged `macos` (there is
  no free shared macOS runner on gitlab.com); someone has to trigger it from
  the pipeline UI, and that runner must be online.
- `release:publish` — waits on both (macOS optional), uploads everything to
  the project's Generic Package Registry, and creates the GitLab Release with
  `SHA256SUMS`.

Required CI/CD variables (Settings → CI/CD → Variables) — full list with
formats/masked/protected settings in
[docs/deploy-gitlab.md](../docs/deploy-gitlab.md):

- `LICENSE_PUBKEY` — base64 Ed25519 public key (`bin/singctl-server keygen`),
  embedded so released builds verify licenses.
- macOS (optional; unset → unsigned artifacts): `APPLE_CERT_P12`,
  `APPLE_CERT_PASSWORD`, `CODESIGN_IDENTITY`, `INSTALLER_IDENTITY`,
  `AC_APPLE_ID`, `AC_PASSWORD`, `AC_TEAM_ID`. On the `macos` runner these are
  normally unnecessary for the identities themselves (assumed to already live
  in that Mac's login keychain) — see the comment in `.gitlab-ci.yml` above
  `release:macos`.

> Windows installers and Linux **arm64** GUI packages are not built yet (the GUI
> needs a native runner per arch); the cross-compiled CLI binaries ship for all
> targets regardless.
