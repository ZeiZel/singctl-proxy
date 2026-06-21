package feature

import (
	"flag"
	"strings"
	"testing"
)

type fakeMod struct {
	d     Descriptor
	bound []string
}

func (m *fakeMod) Descriptor() Descriptor { return m.d }
func (m *fakeMod) Bind(fs *flag.FlagSet) {
	for _, f := range m.d.Flags {
		for _, n := range f.Names {
			fs.Bool(n, false, f.Usage)
		}
	}
}

func sampleRegistry() *Registry {
	keys := &fakeMod{d: Descriptor{
		Name: "keys", Title: "Ключи", Summary: "VLESS-сервер(ы)",
		Flags: []FlagSpec{
			{Names: []string{"k", "key"}, Placeholder: "<vless://...>", Usage: "ключ для подключения", Env: []string{"SINGCTL_KEY", "SINGCTL_KEYS"}, Repeatable: true},
			{Names: []string{"no-save"}, Usage: "не сохранять ключ"},
		},
	}}
	clash := &fakeMod{d: Descriptor{
		Name: "obs", Title: "Наблюдаемость", Summary: "Clash API",
		Flags: []FlagSpec{
			{Names: []string{"clash-api"}, Placeholder: "<host:port>", Usage: "адрес Clash API", Default: "127.0.0.1:9090", Env: []string{"SINGCTL_CLASH_API"}},
		},
	}}
	return New("singctl", "VLESS прокси", "sudo singctl [flags]").Add(keys).Add(clash)
}

func TestHelp_RendersFlagsEnvAndSynopsis(t *testing.T) {
	var b strings.Builder
	sampleRegistry().Help(&b)
	out := b.String()
	for _, want := range []string{
		"sudo singctl [flags]",
		"-k, --key <vless://...>",
		"ключ для подключения",
		"(повторяемый)",
		"--clash-api <host:port>",
		"(по умолчанию: 127.0.0.1:9090)",
		"Environment:",
		"SINGCTL_KEY, SINGCTL_KEYS",
		"SINGCTL_CLASH_API",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q in:\n%s", want, out)
		}
	}
}

func TestBind_RegistersAllNames(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	sampleRegistry().Bind(fs)
	got := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { got[f.Name] = true })
	for _, want := range []string{"k", "key", "no-save", "clash-api"} {
		if !got[want] {
			t.Errorf("flag %q not bound", want)
		}
	}
}

func TestFlagNames_Union(t *testing.T) {
	names := sampleRegistry().FlagNames()
	for _, want := range []string{"k", "key", "no-save", "clash-api"} {
		if !names[want] {
			t.Errorf("FlagNames missing %q", want)
		}
	}
}

func TestMan_RendersSections(t *testing.T) {
	var b strings.Builder
	sampleRegistry().Man(&b)
	out := b.String()
	for _, want := range []string{".TH SINGCTL 1", ".SH NAME", ".SH SYNOPSIS", "-k, --key"} {
		if !strings.Contains(out, want) {
			t.Errorf("man missing %q in:\n%s", want, out)
		}
	}
}
