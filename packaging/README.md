# Packaging & release

Installers for the CLI, native macOS app, and boot-start daemon. Outputs go to
`dist/`. The public source and release origin is
`https://github.com/ZeiZel/singctl-proxy`; build the release artifacts locally,
then attach them to the GitHub release for the matching `vX.Y.Z` tag.

## macOS (.pkg / .dmg, signed locally)

The `.pkg` installs `Singctl.app` (macos/Singctl/, SwiftUI + embedded
ProxyExtension system extension) to `/Applications`, the CLI to
`/usr/local/bin`, and a boot-start **LaunchDaemon** (postinstall pins it to the
console user). The `.dmg` is a drag-install of the app. Developer-ID
distribution, **app sandbox off** (like clash-verge-rev).

```sh
make build                       # CLI (real sing-box core, CGO)
make app-macos                   # Singctl.app, signed at build time
make release-macos RELEASE_VERSION=1.15.0
```

`Singctl.app` is already Developer-ID signed by `make app-macos` (manual
signing pinned in `macos/Singctl/project.yml`); the installer step verifies it.
The default release produces a locally signed `.pkg` and `.dmg` and does not
submit either artifact for notarization. Signing is controlled by these vars:

| env | meaning |
| --- | --- |
| `CODESIGN_IDENTITY` | `Developer ID Application: <Name> (S3UCF4USYC)` — signs the CLI |
| `INSTALLER_IDENTITY` | `Developer ID Installer: <Name> (S3UCF4USYC)` — signs the `.pkg` |
| `NOTARY_PROFILE` | optional `notarytool` keychain profile; omit for the local signed release |
| `AC_APPLE_ID` / `AC_PASSWORD` / `AC_TEAM_ID` | optional notarization credentials; use only when notarization is explicitly required |

The artifacts are `dist/singctl-1.15.0.pkg` and
`dist/singctl-1.15.0.dmg`. The release script checks that the CLI, app,
embedded system extension, package contents, build tags, and installer outputs
all carry the requested version before it returns success.

## Optional GitLab pipeline

`.gitlab-ci.yml` remains available as an optional support pipeline:

- `release:macos` — a **manual** job (`when: manual`, `allow_failure: true`)
  that only runs on a **self-hosted** GitLab Runner tagged `macos` (there is
  no free shared macOS runner on gitlab.com); someone has to trigger it from
  the pipeline UI, and that runner must be online.
- `release:publish` — waits on `release:macos` (optional), uploads artifacts to
  GitLab's Generic Package Registry, and creates a GitLab Release with
  `SHA256SUMS`.

This pipeline does not publish the public GitHub release. GitHub release
artifacts must come from the verified local build and be attached to the
matching GitHub tag/release separately.

Required signing variables are documented in the CI configuration.
- macOS (optional; unset → unsigned artifacts): `APPLE_CERT_P12`,
  `APPLE_CERT_PASSWORD`, `CODESIGN_IDENTITY`, `INSTALLER_IDENTITY`,
  `AC_APPLE_ID`, `AC_PASSWORD`, `AC_TEAM_ID`. On the `macos` runner these are
  normally unnecessary for the identities themselves (assumed to already live
  in that Mac's login keychain) — see the comment in `.gitlab-ci.yml` above
  `release:macos`.

> Windows installers are not built yet; the cross-compiled Windows CLI binary
> ships regardless (`make build-windows`).
