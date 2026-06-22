package procproxy

// output.go is exec-free: it defines the per-app child output capture contract
// (the sink the launch path feeds and the pure line-splitting helpers it uses).
// No os/exec or pipe code lives here so the arch import guard stays satisfied.

// OutputLine is a single line of output captured from a proxied child process.
// Stream is "out" (stdout) or "err" (stderr). App is a short label derived from
// the resolved executable / argv[0] (filepath.Base, see appLabel).
type OutputLine struct {
	PID    int
	App    string
	Stream string
	Text   string
}

// OutputSink receives lines captured from proxied children. Implementations must
// be safe to call from the per-stream reader goroutines (one per child stream).
type OutputSink interface {
	Line(OutputLine)
}

// SinkFunc adapts a plain function to an OutputSink.
type SinkFunc func(OutputLine)

// Line implements OutputSink.
func (f SinkFunc) Line(l OutputLine) { f(l) }

// scannerBufMax is the maximum line length the per-stream scanners accept (~1MB),
// raised well above bufio's 64KiB default so chatty Electron/Chromium logs with
// long lines (stack traces, data URIs) aren't dropped mid-line.
const scannerBufMax = 1 << 20
