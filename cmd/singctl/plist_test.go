package main

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// repoRootForTest locates the module root from this test file's own path (two
// levels up from cmd/singctl), so the test works regardless of the working
// directory `go test` is invoked from.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// cmd/singctl/plist_test.go -> repo root is two directories up.
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// TestInstallerPlists_NoVPNAutostart is F2 item 1's regression test: the
// LaunchDaemon plist BOTH installers (packaging/macos/scripts/postinstall for
// the .pkg, scripts/install-macos.sh for the manual installer) write must
// start the daemon with NO mode — never a hardcoded --vpn — since that,
// combined with KeepAlive, produced the restart loop this fix replaces (F2's
// persisted autostart-mode setting is what re-enables a mode afterwards,
// best-effort, from inside the running daemon).
func TestInstallerPlists_NoVPNAutostart(t *testing.T) {
	root := repoRootForTest(t)
	scripts := []string{
		filepath.Join(root, "packaging", "macos", "scripts", "postinstall"),
		filepath.Join(root, "scripts", "install-macos.sh"),
	}
	for _, path := range scripts {
		t.Run(filepath.Base(path), func(t *testing.T) {
			// Syntax-check the script itself (it's bash, per its shebang).
			out, err := exec.Command("bash", "-n", path).CombinedOutput()
			if err != nil {
				t.Fatalf("bash -n %s: %v\n%s", path, err, out)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			text := string(data)

			if strings.Contains(text, "--vpn") {
				t.Errorf("%s still writes --vpn into the LaunchDaemon's ProgramArguments — "+
					"the daemon must start with no mode (F2 item 1); autostart is applied "+
					"from inside the running daemon instead", path)
			}
			if !strings.Contains(text, "<string>--headless</string>") {
				t.Errorf("%s does not write --headless into ProgramArguments", path)
			}
			if !strings.Contains(text, "<key>KeepAlive</key>") || !strings.Contains(text, "<true/>") {
				t.Errorf("%s: KeepAlive should stay enabled — F2's fix is that a failed mode "+
					"never exits, not that the daemon stops being kept alive", path)
			}
		})
	}
}
