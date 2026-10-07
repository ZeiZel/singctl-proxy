# SingctlPreview

`SingctlPreview` is a command-line screenshot renderer. It compiles the real
SwiftUI `App` sources with `SCREENSHOT_HARNESS`, which omits the shipping app
entry point. The only backend is the fixed in-memory `DemoBackend` in
`main.swift`; it never constructs `DaemonBackend`, accesses control sockets,
starts the daemon, changes proxy settings, or needs credentials.

From `macos/Singctl` on macOS:

```sh
xcodegen generate
xcodebuild -project Singctl.xcodeproj -scheme SingctlPreview -configuration Debug -derivedDataPath /tmp/singctl-dark-preview-build CODE_SIGNING_ALLOWED=NO build
/tmp/singctl-dark-preview-build/Build/Products/Debug/SingctlPreview --screen dashboard --output ../../docs/images/singctl-swiftui-dashboard.png
```

Run the production-source energy harness from the same build product:

```sh
/tmp/singctl-dark-preview-build/Build/Products/Debug/SingctlPreview --energy-checks
```

The harness uses an in-memory `Backend` and the real `LiveStore`,
`LogsModel`, and segmented-control implementation. It checks hidden-window
backend gating, publication before a suspended latency request, zero-rate
samples, retention and draining of more than 2,000 hidden console lines, and
rejected picker resynchronisation. It does not access a daemon, control
socket, network, or proxy settings.

The control-client lifecycle fixture is a separate Foundation-only executable;
it injects endpoint discovery and transport and never opens a socket:

```sh
/Library/Developer/CommandLineTools/usr/bin/swiftc -sdk /Library/Developer/CommandLineTools/SDKs/MacOSX.sdk \
  -target arm64-apple-macos26 -D CONTROL_CLIENT_FIXTURE \
  App/Core/Models.swift App/Core/InstanceDiscovery.swift App/Core/ControlClient.swift \
  PreviewHarness/ControlClientFixture.swift -o /tmp/control-client-fixture
/tmp/control-client-fixture
```

The default and README hero appearance is dark. Pass `--appearance light`
explicitly for a Light capture. For visual QA, choose `--screen settings` or
`--width narrow`; point `--output` at `/tmp/` to keep the checked-in hero
unchanged. SwiftUI materials are composited by WindowServer on current macOS,
so the renderer captures its own fixed-size, borderless harness window with
`screencapture -l <its-window-id>` after it has loaded the fixture. It never
captures the desktop or another app, and its complete background is rendered
by `PreviewPresentation` in `main.swift`. If macOS denies the capture, grant
Screen Recording permission to the terminal or Xcode process that runs this
fixture, then rerun the same command.
