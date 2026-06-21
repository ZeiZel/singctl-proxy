//go:build !linux

package procproxy

// NewRouter on non-Linux platforms returns the env-injection fallback: Launch
// starts a child with proxy env set; per-PID routing of existing processes is
// unsupported (would need a signed network/system extension on macOS).
func NewRouter(cfg Config) Router { return newEnvRouter(cfg) }
