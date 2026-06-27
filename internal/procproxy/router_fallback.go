//go:build !linux && !darwin

package procproxy

// NewRouter on platforms without a native per-app backend (currently Windows)
// returns the env-injection fallback: Launch starts a child with proxy env set;
// per-PID routing of existing processes is unsupported. Linux has the cgroup
// router (router_linux.go); macOS has the system-extension router
// (router_darwin.go).
func NewRouter(cfg Config) Router { return newEnvRouter(cfg) }
