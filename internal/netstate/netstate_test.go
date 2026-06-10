package netstate

import (
	"context"
	"strings"
	"testing"
)

// --- captured/synthetic fixtures ---

const ifconfigCisco = `en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	inet6 fe80::48a:887b:b997:f76d%en0 prefixlen 64 secured scopeid 0xe
	inet 192.168.1.148 netmask 0xffffff00 broadcast 192.168.1.255
utun0: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1500
	inet6 fe80::baf8:f82b:202b:48a2%utun0 prefixlen 64 scopeid 0x12
utun4: flags=80d1<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST> mtu 1300
	inet 172.18.113.59 --> 172.18.113.59 netmask 0xfffffc00
	inet6 fe80::862f:57ff:fe84:41e3%utun4 prefixlen 64 scopeid 0x16
`

const routeDefaultCisco = `   route to: default
destination: default
       mask: default
  interface: utun4
      flags: <UP,DONE,CLONING,STATIC,GLOBAL>
`

const netstatCisco = `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            link#22            UCSg                utun4
default            192.168.1.1        UGScIg                en0
127                127.0.0.1          UCS                   lo0
`

const psCisco = `  534 vpnagentd
  569 com.cisco.anyconnect.macos.acsockext
 1963 Cisco Secure Client
`

const ifconfigNoCisco = `en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	inet 192.168.1.148 netmask 0xffffff00 broadcast 192.168.1.255
utun0: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1500
	inet6 fe80::baf8:f82b:202b:48a2%utun0 prefixlen 64 scopeid 0x12
`

const routeDefaultNoCisco = `   route to: default
  interface: en0
`

const netstatNoCisco = `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            192.168.1.1        UGScg                 en0
127                127.0.0.1          UCS                   lo0
`

const psNoCisco = `  100 launchd
  200 Finder
`

// our forwarder TUN up (198.18.0.1), no Cisco.
const ifconfigOurTun = `en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	inet 192.168.1.148 netmask 0xffffff00 broadcast 192.168.1.255
utun5: flags=80d1<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST> mtu 9000
	inet 198.18.0.1 --> 198.18.0.1 netmask 0xfffffffc
`

const routeDefaultOurTun = `  interface: utun5
`

const netstatOurTun = `Internet:
Destination        Gateway            Flags               Netif Expire
default            link#23            UCSg                utun5
default            192.168.1.1        UGScIg                en0
`

// both Cisco (utun4) and our TUN (utun5) present.
const ifconfigBoth = ifconfigCisco + `utun5: flags=80d1<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST> mtu 9000
	inet 198.18.0.1 --> 198.18.0.1 netmask 0xfffffffc
`

type scenario struct {
	ifconfig, route, netstat, ps string
}

func runnerFor(s scenario) CommandRunner {
	return fakeRunner{out: map[string][]byte{
		"ifconfig":             []byte(s.ifconfig),
		"route -n get default": []byte(s.route),
		"netstat -rn -f inet":  []byte(s.netstat),
		"ps -axo pid,comm":     []byte(s.ps),
	}}
}

type fakeRunner struct {
	out map[string][]byte
	err map[string]error
}

func (f fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name
	if len(args) > 0 {
		key = name + " " + strings.Join(args, " ")
	}
	if f.err != nil {
		if e := f.err[key]; e != nil {
			return nil, e
		}
	}
	return f.out[key], nil
}

// --- Observe scenarios ---

func TestObserve_CiscoConnected(t *testing.T) {
	ns, err := New(runnerFor(scenario{ifconfigCisco, routeDefaultCisco, netstatCisco, psCisco})).Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ns.CiscoActive {
		t.Error("want CiscoActive=true")
	}
	if ns.DefaultRouteIface != "utun4" {
		t.Errorf("DefaultRouteIface = %q, want utun4", ns.DefaultRouteIface)
	}
	if ns.PhysicalIface != "en0" {
		t.Errorf("PhysicalIface = %q, want en0", ns.PhysicalIface)
	}
	if !ns.CiscoProcessPresent {
		t.Error("want CiscoProcessPresent=true")
	}
}

func TestObserve_NoCisco(t *testing.T) {
	ns, err := New(runnerFor(scenario{ifconfigNoCisco, routeDefaultNoCisco, netstatNoCisco, psNoCisco})).Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ns.CiscoActive {
		t.Error("want CiscoActive=false")
	}
	if ns.DefaultRouteIface != "en0" || ns.PhysicalIface != "en0" {
		t.Errorf("ifaces = default %q phys %q, want en0/en0", ns.DefaultRouteIface, ns.PhysicalIface)
	}
	if ns.CiscoProcessPresent {
		t.Error("want CiscoProcessPresent=false")
	}
}

func TestObserve_OurTunUp_NotCisco(t *testing.T) {
	ns, err := New(runnerFor(scenario{ifconfigOurTun, routeDefaultOurTun, netstatOurTun, psNoCisco})).Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ns.CiscoActive {
		t.Error("our own forwarder TUN must NOT be classified as Cisco")
	}
	if ns.PhysicalIface != "en0" {
		t.Errorf("PhysicalIface = %q, want en0", ns.PhysicalIface)
	}
	var ours bool
	for _, tn := range ns.Tunnels {
		if tn.Name == "utun5" && tn.IsOurs {
			ours = true
		}
	}
	if !ours {
		t.Error("utun5 (198.18.0.1) should be flagged IsOurs")
	}
}

func TestObserve_OurTunAndCiscoBoth(t *testing.T) {
	ns, err := New(runnerFor(scenario{ifconfigBoth, routeDefaultCisco, netstatCisco, psCisco})).Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ns.CiscoActive {
		t.Error("Cisco (utun4) must still be detected even with our TUN (utun5) up")
	}
}

// --- pure parser tests ---

func TestParseIfconfig(t *testing.T) {
	ifaces := ParseIfconfig([]byte(ifconfigCisco))
	by := map[string]IfaceInfo{}
	for _, i := range ifaces {
		by[i.Name] = i
	}
	if u := by["utun4"]; !u.NoARP || len(u.IPv4) != 1 || u.IPv4[0] != "172.18.113.59" {
		t.Errorf("utun4 = %+v, want NoARP + inet 172.18.113.59", u)
	}
	if e := by["en0"]; len(e.IPv4) != 1 || e.IPv4[0] != "192.168.1.148" || e.NoARP {
		t.Errorf("en0 = %+v, want inet 192.168.1.148 no NoARP", e)
	}
	if u0 := by["utun0"]; len(u0.IPv4) != 0 {
		t.Errorf("utun0 = %+v, want no IPv4 (link-local only)", u0)
	}
}

func TestParseRouteGetDefault(t *testing.T) {
	if got := ParseRouteGetDefault([]byte(routeDefaultCisco)); got != "utun4" {
		t.Errorf("got %q, want utun4", got)
	}
	if got := ParseRouteGetDefault([]byte("no interface line here")); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestParseNetstatDefaults(t *testing.T) {
	rows := ParseNetstatDefaults([]byte(netstatCisco))
	if len(rows) != 2 {
		t.Fatalf("got %d default rows, want 2", len(rows))
	}
	if rows[0].Iface != "utun4" || rows[0].IsIPGateway {
		t.Errorf("row0 = %+v, want utun4 link gateway", rows[0])
	}
	if rows[1].Iface != "en0" || !rows[1].IsIPGateway || rows[1].Gateway != "192.168.1.1" {
		t.Errorf("row1 = %+v, want en0 192.168.1.1", rows[1])
	}
}

func TestPickPhysical(t *testing.T) {
	if got := pickPhysical(ParseNetstatDefaults([]byte(netstatCisco))); got != "en0" {
		t.Errorf("got %q, want en0", got)
	}
}

func TestClassifyTunnels(t *testing.T) {
	tuns := classifyTunnels(ParseIfconfig([]byte(ifconfigBoth)), "utun4", OurTunAddrPrefix)
	by := map[string]bool{} // name -> IsOurs
	foreignActive := false
	for _, t := range tuns {
		by[t.Name] = t.IsOurs
		if !t.IsOurs && t.HasIPv4 && (t.NoARP || t.OwnsDefault) {
			foreignActive = true
		}
	}
	if by["utun5"] != true {
		t.Error("utun5 should be ours")
	}
	if by["utun4"] != false {
		t.Error("utun4 should be foreign")
	}
	if !foreignActive {
		t.Error("expected a foreign active tunnel (utun4)")
	}
}

func TestParseCiscoProcs(t *testing.T) {
	if !parseCiscoProcs([]byte(psCisco)) {
		t.Error("want true for Cisco process list")
	}
	if parseCiscoProcs([]byte(psNoCisco)) {
		t.Error("want false for non-Cisco process list")
	}
}
