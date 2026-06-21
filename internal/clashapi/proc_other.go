//go:build !linux

package clashapi

// NewProcessResolver has no /proc to read on non-Linux platforms, so it returns
// a resolver that never resolves. sing-box's own process field (when populated)
// is still used; otherwise the process shows as unknown.
func NewProcessResolver() func(srcPort int) string {
	return func(int) string { return "" }
}
