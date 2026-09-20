// Package protocol is the contract every proxy protocol singctl speaks is
// expressed through, plus the registry that dispatches to them.
//
// A protocol module owns its whole vertical slice: it parses its own input and
// renders its own engine node. Nothing outside a module knows the shape of its
// parameters, which is what makes Profile.Params an `any` rather than a
// liability — only the module that produced a Profile ever type-asserts it.
//
// This package deliberately imports no engine. Rendering is expressed as a
// capability interface next to the engine that consumes it (see
// internal/singbox.Renderer), so a protocol package pulls in an engine's schema
// only when it actually targets that engine.
//
// There is no init()-time registration anywhere. The registry is assembled
// explicitly in one composition root and injected, so the set of supported
// protocols is a property of the program's wiring rather than of which packages
// happened to be linked in.
package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Name identifies a protocol, e.g. "vless" or "wireguard".
type Name string

// Kind says how an engine must place a protocol's config node. sing-box models
// most protocols as outbounds, but WireGuard-family ones as endpoints — a
// sibling array at the top level whose tags are usable wherever an outbound tag
// is, so both kinds can share a failover group.
type Kind uint8

const (
	KindOutbound Kind = iota
	KindEndpoint
)

func (k Kind) String() string {
	if k == KindEndpoint {
		return "endpoint"
	}
	return "outbound"
}

// Input says how a key reaches singctl.
type Input uint8

const (
	// InputLink is a share URL whose scheme names the protocol. This is the
	// common case, and it is why singctl has no protocol picker: the key
	// describes itself, so a user cannot pick one protocol and paste another.
	InputLink Input = iota
	// InputConfig is a configuration blob with no scheme to dispatch on — a
	// WireGuard INI file, say. These are the only inputs that need the user to
	// say what they are handing over.
	InputConfig
)

// Descriptor is a module's self-description.
type Descriptor struct {
	Name    Name
	Title   string   // display name, e.g. "WireGuard"
	Kind    Kind     //
	Input   Input    //
	Schemes []string // URL schemes claimed (InputLink only), lowercase, no "://"
}

// Profile is one parsed server, engine-agnostic.
type Profile struct {
	Protocol Name
	Label    string // human-readable name, for the UI
	Raw      string // the original input, kept for display and re-parse
	Params   any    // protocol-specific payload, owned by the parsing module
}

// Module is one protocol adapter.
type Module interface {
	Descriptor() Descriptor
	Parse(raw string) (Profile, error)
}

// Relabeler is implemented by modules whose input format can carry a new
// display name. Every link-based protocol can (the URL fragment, or vmess's
// "ps" field); a config blob generally cannot, and the registry reports that
// rather than silently doing nothing.
type Relabeler interface {
	SetLabel(raw, label string) (string, error)
}

// Sniffer is implemented by InputConfig modules so the registry can recognise
// their blobs. It must be cheap and conservative: returning true for input the
// module cannot actually parse turns a helpful "unsupported" message into a
// confusing parse error.
type Sniffer interface {
	Sniff(raw string) bool
}

// Errors reported by the registry itself. Module-specific failures are the
// modules' own errors, returned unwrapped.
var (
	ErrUnsupported   = errors.New("unsupported input")
	ErrNoModules     = errors.New("no protocol modules registered")
	ErrCannotRelabel = errors.New("this protocol's keys cannot be renamed")
)

// Registry dispatches raw input to the module that claims it.
type Registry struct {
	modules  []Module
	byName   map[Name]Module
	byScheme map[string]Module
	configs  []Module // InputConfig modules, in registration order

	// linkRe matches the start of any registered scheme. Splitting a pasted
	// blob has to anchor on where the NEXT link begins, never on whitespace:
	// a share link's #fragment is a human label and routinely contains spaces
	// ("#Poland 🇵🇱", "#Auto → [🚀 Optimal]"). Splitting on whitespace shreds
	// those into shards of a label, and the first shard reaches the parser as
	// a bogus key.
	linkRe *regexp.Regexp
}

// NewRegistry builds a registry from an explicit module list. It rejects
// duplicate names and duplicate schemes: two modules claiming "vless://" is a
// wiring mistake that must fail at startup, not resolve arbitrarily at runtime.
func NewRegistry(modules ...Module) (*Registry, error) {
	if len(modules) == 0 {
		return nil, ErrNoModules
	}
	r := &Registry{
		byName:   make(map[Name]Module, len(modules)),
		byScheme: make(map[string]Module, len(modules)*2),
	}
	for _, m := range modules {
		d := m.Descriptor()
		if d.Name == "" {
			return nil, fmt.Errorf("protocol: module %T has an empty name", m)
		}
		if _, dup := r.byName[d.Name]; dup {
			return nil, fmt.Errorf("protocol: duplicate module name %q", d.Name)
		}
		switch d.Input {
		case InputLink:
			if len(d.Schemes) == 0 {
				return nil, fmt.Errorf("protocol: link module %q declares no schemes", d.Name)
			}
			for _, s := range d.Schemes {
				s = strings.ToLower(s)
				if prev, dup := r.byScheme[s]; dup {
					return nil, fmt.Errorf("protocol: scheme %q claimed by both %q and %q",
						s, prev.Descriptor().Name, d.Name)
				}
				r.byScheme[s] = m
			}
		case InputConfig:
			if _, ok := m.(Sniffer); !ok {
				return nil, fmt.Errorf("protocol: config module %q must implement Sniffer", d.Name)
			}
			r.configs = append(r.configs, m)
		}
		r.byName[d.Name] = m
		r.modules = append(r.modules, m)
	}
	r.linkRe = compileSchemeRe(r.Schemes())
	return r, nil
}

// compileSchemeRe builds the "start of a link" matcher. Schemes are sorted
// longest-first so an alternation can never let a prefix win over the scheme
// that actually applies (hysteria before hysteria2 would mis-split every
// hysteria2:// link).
func compileSchemeRe(schemes []string) *regexp.Regexp {
	if len(schemes) == 0 {
		return nil
	}
	sorted := append([]string(nil), schemes...)
	sort.Slice(sorted, func(i, j int) bool {
		if len(sorted[i]) != len(sorted[j]) {
			return len(sorted[i]) > len(sorted[j])
		}
		return sorted[i] < sorted[j]
	})
	for i, s := range sorted {
		sorted[i] = regexp.QuoteMeta(s)
	}
	return regexp.MustCompile(`(?i)\b(` + strings.Join(sorted, "|") + `)://`)
}

// Parse dispatches raw input to whichever module claims it: by URL scheme for
// share links, else by asking each config module to sniff it.
func (r *Registry) Parse(raw string) (Profile, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Profile{}, r.unsupported("")
	}
	if scheme, _, found := strings.Cut(trimmed, "://"); found {
		if m, ok := r.byScheme[strings.ToLower(scheme)]; ok {
			return m.Parse(trimmed)
		}
		// A scheme we do not know is reported as such rather than offered to
		// the config sniffers: "https://…" is far more likely to be a
		// subscription URL pasted by mistake than a config blob.
		return Profile{}, r.unsupported(scheme)
	}
	for _, m := range r.configs {
		if m.(Sniffer).Sniff(trimmed) {
			return m.Parse(trimmed)
		}
	}
	return Profile{}, r.unsupported("")
}

// SetLabel renames a key in place, delegating to the module that owns it.
func (r *Registry) SetLabel(raw, label string) (string, error) {
	p, err := r.Parse(raw)
	if err != nil {
		return "", err
	}
	m, ok := r.byName[p.Protocol]
	if !ok {
		return "", r.unsupported(string(p.Protocol))
	}
	rl, ok := m.(Relabeler)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrCannotRelabel, m.Descriptor().Title)
	}
	return rl.SetLabel(strings.TrimSpace(raw), strings.TrimSpace(label))
}

// Module looks a module up by protocol name.
func (r *Registry) Module(name Name) (Module, bool) {
	m, ok := r.byName[name]
	return m, ok
}

// Modules returns every registered module, in registration order.
func (r *Registry) Modules() []Module {
	out := make([]Module, len(r.modules))
	copy(out, r.modules)
	return out
}

// Schemes returns every accepted URL scheme, sorted — for help and error text,
// so the list can never drift from what is actually registered.
func (r *Registry) Schemes() []string {
	out := make([]string, 0, len(r.byScheme))
	for s := range r.byScheme {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) unsupported(got string) error {
	schemes := r.Schemes()
	for i, s := range schemes {
		schemes[i] = s + "://"
	}
	if got != "" {
		return fmt.Errorf("%w %q: expected one of %s", ErrUnsupported, got, strings.Join(schemes, ", "))
	}
	return fmt.Errorf("%w: expected one of %s (or a WireGuard config)", ErrUnsupported, strings.Join(schemes, ", "))
}
