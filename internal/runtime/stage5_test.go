package runtime

import (
	"context"
	"strings"
	"testing"

	"singctl/internal/netstate"
	"singctl/internal/vless"
)

const realLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=k&sid=4d04&sni=cursor.com&fp=chrome#t"

func TestProfileConfigBuilder_BindAndForwarder(t *testing.T) {
	p, err := vless.ParseLink(realLink)
	if err != nil {
		t.Fatal(err)
	}
	b := ProfileConfigBuilder{Profiles: vless.SingleSet(p)}

	proxyVPN, err := b.ProxyConfig("en0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(proxyVPN), `"bind_interface": "en0"`) {
		t.Error("VPN-mode proxy config must bind outbounds to en0")
	}

	proxyOnly, _ := b.ProxyConfig("")
	if strings.Contains(string(proxyOnly), "bind_interface") {
		t.Error("proxy-only config must not bind")
	}

	fwd, err := b.ForwarderConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fwd), `"server": "127.0.0.1"`) || !strings.Contains(string(fwd), `"server_port": 1080`) {
		t.Error("forwarder must relay to 127.0.0.1:1080")
	}
	if !strings.Contains(string(fwd), `"type": "tun"`) {
		t.Error("forwarder must have a tun inbound")
	}
}

// fakeRunner is a runtime-local CommandRunner over canned fixtures.
type fakeRunner struct{ out map[string][]byte }

func (f fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name
	if len(args) > 0 {
		key = name + " " + strings.Join(args, " ")
	}
	return f.out[key], nil
}

const ifconfigCiscoAndOurs = `en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	inet 192.168.1.148 netmask 0xffffff00 broadcast 192.168.1.255
utun4: flags=80d1<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST> mtu 1300
	inet 172.18.113.59 --> 172.18.113.59 netmask 0xfffffc00
utun7: flags=80d1<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST> mtu 9000
	inet 198.18.0.1 --> 198.18.0.1 netmask 0xfffffffc
`

const netstatCiscoPrimary = `Internet:
Destination        Gateway            Flags               Netif Expire
default            link#22            UCSg                utun4
default            192.168.1.1        UGScIg                en0
`

func TestOrphanTunDevices_NeverMatchesCisco(t *testing.T) {
	ifaces := netstate.ParseIfconfig([]byte(ifconfigCiscoAndOurs))
	got := OrphanTunDevices(ifaces, netstate.OurTunAddrPrefix)
	if len(got) != 1 || got[0] != "utun7" {
		t.Fatalf("OrphanTunDevices = %v, want [utun7] only (never Cisco's utun4)", got)
	}
	for _, d := range got {
		if d == "utun4" {
			t.Fatal("must never select Cisco's tunnel for destruction")
		}
	}
}

func TestNetProber_PhysicalDefault_IgnoresCiscoGlobalPrimary(t *testing.T) {
	r := fakeRunner{out: map[string][]byte{
		"ifconfig":             []byte(ifconfigCiscoAndOurs),
		"netstat -rn -f inet":  []byte(netstatCiscoPrimary),
		"route -n get default": []byte("  interface: utun4\n"),
		"ps -axo pid,comm":     []byte("534 vpnagentd\n"),
	}}
	prober := NewNetProber(netstate.New(r))
	got, err := prober.PhysicalDefault()
	if err != nil {
		t.Fatal(err)
	}
	if got != "en0" {
		t.Fatalf("PhysicalDefault = %q, want en0 (ignore Cisco's utun4 primary)", got)
	}
}
