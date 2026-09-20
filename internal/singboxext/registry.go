//go:build singbox

package singboxext

import (
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/include"
)

// OutboundRegistry returns sing-box's stock outbound registry extended with
// singctl's own types. Every place that builds a sing-box context must use this
// instead of include.OutboundRegistry(), or configs naming our types fail to
// decode with "outbound type not found".
func OutboundRegistry() *outbound.Registry {
	registry := include.OutboundRegistry()
	RegisterOutbound(registry)
	return registry
}
