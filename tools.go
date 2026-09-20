//go:build tools

// This file exists only to keep `go mod tidy` from dropping build-time TOOLS
// that no production package imports.
//
// `make libbox` runs `go install github.com/sagernet/gomobile/cmd/{gomobile,gobind}`
// WITHOUT an @version, which resolves the tool from this module's own
// requirements — so once tidy removes them, the App Store SKU's Libbox build
// fails with "missing go.sum entry". Nothing here is compiled into any binary:
// the `tools` build tag is never set for a real build.
package tools

import (
	_ "github.com/sagernet/gomobile/cmd/gobind"
	_ "github.com/sagernet/gomobile/cmd/gomobile"
)
