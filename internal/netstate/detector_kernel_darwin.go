//go:build darwin

package netstate

import (
	"context"
	"fmt"
	"net"
	"sort"
	"syscall"

	"golang.org/x/net/route"
	"singctl/internal/types"
)

// kernelDetector reads the Darwin routing and interface information bases.
// FetchRIB uses sysctl and never sends a routing request, so observing state
// cannot generate RTM_GET messages for WatchRouteChanges.
type kernelDetector struct{ ourAddrPrefix string }

// NewKernel returns the production Darwin detector. It has no subprocess or
// process-list dependency.
func NewKernel() ObservationSource { return &kernelDetector{ourAddrPrefix: OurTunAddrPrefix} }

func (d *kernelDetector) Observe(ctx context.Context) (types.NetState, error) {
	select {
	case <-ctx.Done():
		return types.NetState{}, ctx.Err()
	default:
	}
	ifaces, err := kernelInterfaces()
	if err != nil {
		return types.NetState{}, err
	}
	routes, err := kernelRoutes()
	if err != nil {
		return types.NetState{}, err
	}
	return d.snapshot(ifaces, routes), nil
}

func (d *kernelDetector) snapshot(ifaces map[string]IfaceInfo, routes []kernelRoute) types.NetState {
	defIface := ""
	defaults := make([]RouteRow, 0, len(routes))
	for _, r := range routes {
		if r.defaultRoute && !r.scoped && defIface == "" {
			defIface = r.iface
		}
		if r.defaultRoute {
			defaults = append(defaults, RouteRow{Iface: r.iface, IsIPGateway: r.gatewayIP})
		}
	}
	names := make([]string, 0, len(ifaces))
	for name := range ifaces {
		names = append(names, name)
	}
	sort.Strings(names)
	parsed := make([]IfaceInfo, 0, len(names))
	for _, name := range names {
		parsed = append(parsed, ifaces[name])
	}
	tunnels := classifyTunnels(parsed, defIface, d.ourAddrPrefix)
	ciscoActive, ciscoOwns := false, false
	for _, t := range tunnels {
		if t.IsOurs {
			continue
		}
		if t.HasIPv4 && (t.NoARP || t.OwnsDefault) {
			ciscoActive = true
		}
		if t.OwnsDefault {
			ciscoOwns = true
		}
	}
	return types.NetState{
		DefaultRouteIface: defIface,
		PhysicalIface:     pickPhysical(defaults),
		Tunnels:           tunnels,
		CiscoActive:       ciscoActive,
		CiscoOwnsDefault:  ciscoOwns,
	}
}

type kernelRoute struct {
	iface        string
	defaultRoute bool
	gatewayIP    bool
	scoped       bool
}

func kernelRoutes() ([]kernelRoute, error) {
	b, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, fmt.Errorf("fetch route RIB: %w", err)
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, b)
	if err != nil {
		return nil, fmt.Errorf("parse route RIB: %w", err)
	}
	names := map[int]string{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("route interface names: %w", err)
	}
	for _, iface := range interfaces {
		names[iface.Index] = iface.Name
	}
	return parseKernelRoutes(msgs, names), nil
}

func parseKernelRoutes(msgs []route.Message, indexes ...map[int]string) []kernelRoute {
	var out []kernelRoute
	for _, msg := range msgs {
		rm, ok := msg.(*route.RouteMessage)
		if !ok || rm.Err != nil || rm.Flags&syscall.RTF_HOST != 0 || rm.Flags&syscall.RTF_UP == 0 {
			continue
		}
		if len(rm.Addrs) <= syscall.RTAX_DST {
			continue
		}
		dst, ok := rm.Addrs[syscall.RTAX_DST].(*route.Inet4Addr)
		if !ok || dst.IP != [4]byte{} {
			continue
		}
		// Darwin commonly omits RTAX_NETMASK for an implicit /0 route.
		// If present, it must still describe /0; a non-zero mask is a
		// more-specific route whose destination happened to be zero.
		if len(rm.Addrs) > syscall.RTAX_NETMASK && rm.Addrs[syscall.RTAX_NETMASK] != nil {
			mask, ok := rm.Addrs[syscall.RTAX_NETMASK].(*route.Inet4Addr)
			if !ok || mask.IP != [4]byte{} {
				continue
			}
		}
		name := ""
		if len(rm.Addrs) > syscall.RTAX_IFP {
			if link, ok := rm.Addrs[syscall.RTAX_IFP].(*route.LinkAddr); ok {
				name = link.Name
				if name == "" && len(indexes) > 0 && link.Index != 0 {
					name = indexes[0][link.Index]
				}
			}
		}
		if name == "" && len(indexes) > 0 {
			name = indexes[0][rm.Index]
		}
		if name == "" {
			continue
		}
		gatewayIP := false
		if len(rm.Addrs) > syscall.RTAX_GATEWAY {
			_, gatewayIP = rm.Addrs[syscall.RTAX_GATEWAY].(*route.Inet4Addr)
		}
		out = append(out, kernelRoute{iface: name, defaultRoute: true, gatewayIP: gatewayIP, scoped: rm.Flags&syscall.RTF_IFSCOPE != 0})
	}
	return out
}

func kernelInterfaces() (map[string]IfaceInfo, error) {
	// net.Interfaces supplies stable names and address conversion. Darwin's
	// route interface RIB supplies NOARP, which net.Interface does not expose.
	ints, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	out := make(map[string]IfaceInfo, len(ints))
	names := make(map[int]string, len(ints))
	for _, in := range ints {
		info := IfaceInfo{Name: in.Name, Up: in.Flags&net.FlagUp != 0, PointToPoint: in.Flags&net.FlagPointToPoint != 0}
		addrs, addrErr := in.Addrs()
		if addrErr != nil {
			return nil, fmt.Errorf("interface %s addresses: %w", in.Name, addrErr)
		}
		for _, addr := range addrs {
			ip, _, e := net.ParseCIDR(addr.String())
			if e == nil && ip.To4() != nil {
				info.IPv4 = append(info.IPv4, ip.To4().String())
			}
		}
		out[in.Name] = info
		names[in.Index] = in.Name
	}
	b, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeInterface, 0)
	if err != nil {
		return nil, fmt.Errorf("fetch interface RIB: %w", err)
	}
	msgs, err := route.ParseRIB(route.RIBTypeInterface, b)
	if err != nil {
		return nil, fmt.Errorf("parse interface RIB: %w", err)
	}
	seen := map[string]bool{}
	for _, msg := range msgs {
		im, ok := msg.(*route.InterfaceMessage)
		if !ok {
			continue
		}
		name := im.Name
		if name == "" {
			name = names[im.Index]
		}
		if info, ok := out[name]; ok {
			info.NoARP = im.Flags&syscall.IFF_NOARP != 0
			out[name] = info
			seen[name] = true
		}
	}
	for name := range out {
		if isTunnelName(name) && !seen[name] {
			return nil, fmt.Errorf("interface %s absent from flags RIB", name)
		}
	}
	return out, nil
}
