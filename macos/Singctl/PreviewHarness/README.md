# SingctlPreview

`SingctlPreview` is a command-line screenshot renderer. It compiles the real
SwiftUI `App` sources with `SCREENSHOT_HARNESS`, which omits the shipping app
entry point. The only backend is the fixed in-memory `DemoBackend` in
`main.swift`; it never constructs `DaemonBackend`, accesses control sockets,
starts the daemon, changes proxy settings, or needs credentials.

From `macos/Singctl` on macOS:

```sh
xcodegen generate
xcodebuild -project Singctl.xcodeproj -scheme SingctlPreview -configuration Debug -derivedDataPath build CODE_SIGNING_ALLOWED=NO build
./build/Build/Products/Debug/SingctlPreview --screen dashboard --appearance light --output ../../docs/images/singctl-swiftui-dashboard.png
```

For visual QA, choose `--screen settings`, `--appearance dark`, or
`--width narrow`; point `--output` at `/tmp/` to keep the checked-in hero
unchanged. SwiftUI materials are composited by WindowServer on current macOS,
so the renderer captures its own fixed-size, borderless harness window with
`screencapture -l <its-window-id>` after it has loaded the fixture. It never
captures the desktop or another app, and its complete background is rendered
by `PreviewPresentation` in `main.swift`. If macOS denies the capture, grant
Screen Recording permission to the terminal or Xcode process that runs this
fixture, then rerun the same command.
