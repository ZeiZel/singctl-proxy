package singbox

import "testing"

func TestGenerateProxyConfigPorts_CustomPorts(t *testing.T) {
	cfg, err := GenerateProxyConfigPorts(mustProfile(t), "", Ports{Socks: 7890, HTTP: 7891})
	if err != nil {
		t.Fatal(err)
	}
	socks := cfg.Inbounds[0].(SocksInbound)
	http := cfg.Inbounds[1].(HTTPInbound)
	if socks.ListenPort != 7890 || http.ListenPort != 7891 {
		t.Errorf("inbound ports = %d/%d, want 7890/7891", socks.ListenPort, http.ListenPort)
	}
}

func TestGenerateForwarderConfigPorts_DialsCustomSocks(t *testing.T) {
	cfg, err := GenerateForwarderConfigPorts(mustProfile(t), Ports{Socks: 7890})
	if err != nil {
		t.Fatal(err)
	}
	var out *SocksOutbound
	for _, o := range cfg.Outbounds {
		if s, ok := o.(SocksOutbound); ok {
			out = &s
			break
		}
	}
	if out == nil {
		t.Fatal("forwarder has no socks outbound")
	}
	if out.ServerPort != 7890 {
		t.Errorf("forwarder dials socks port %d, want 7890", out.ServerPort)
	}
}

func TestPorts_ZeroValueKeepsDefaults(t *testing.T) {
	cfg, err := GenerateProxyConfigPorts(mustProfile(t), "", Ports{})
	if err != nil {
		t.Fatal(err)
	}
	socks := cfg.Inbounds[0].(SocksInbound)
	http := cfg.Inbounds[1].(HTTPInbound)
	if socks.ListenPort != 1080 || http.ListenPort != 2080 {
		t.Errorf("zero Ports = %d/%d, want defaults 1080/2080", socks.ListenPort, http.ListenPort)
	}
}
