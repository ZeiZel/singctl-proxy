// Package feature is a small, dependency-free module framework for the CLI. Each
// user-facing capability is a Module that describes itself (name, docs, flags)
// and binds its own flags into a flag.FlagSet. A root Registry assembles the
// modules and GENERATES the --help/usage and man text from their descriptors, so
// there is no hand-maintained usage string. It imports only the standard library
// (notably never internal/app), so any package can expose a Descriptor without
// risking an import cycle.
package feature

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// FlagSpec documents a single flag. It is the source of truth for help/man text;
// a module's Bind must register exactly these Names (guarded by a parity test).
type FlagSpec struct {
	Names       []string // canonical first; 1-char names render as -x, longer as --xx
	Placeholder string   // e.g. "<vless://...>"; empty for boolean flags
	Usage       string
	Default     string   // human-readable default (shown in help when non-empty)
	Env         []string // associated environment variables (documented separately)
	Repeatable  bool     // documents that the flag may be given more than once
}

// Descriptor is a module's static self-description.
type Descriptor struct {
	Name    string // stable id, e.g. "keys"
	Title   string // human title, e.g. "Ключи (VLESS)"
	Summary string // one-line summary shown beside the title
	Doc     string // long-form prose (man page / future use)
	Flags   []FlagSpec
}

// Module is a user-facing feature: it describes itself and binds its flags.
// Execution/wiring is intentionally NOT part of this interface so the package
// stays free of any dependency on the runtime (the composition root performs
// wiring against the concrete module types).
type Module interface {
	Descriptor() Descriptor
	Bind(fs *flag.FlagSet)
}

// Registry assembles modules and renders help/man from their descriptors.
type Registry struct {
	name     string // program name, e.g. "singctl"
	tagline  string // one-line program description
	synopsis string // usage synopsis, e.g. "sudo singctl [flags]"
	mods     []Module
}

// New creates a registry for a program.
func New(name, tagline, synopsis string) *Registry {
	return &Registry{name: name, tagline: tagline, synopsis: synopsis}
}

// Add registers a module (chainable).
func (r *Registry) Add(m Module) *Registry {
	r.mods = append(r.mods, m)
	return r
}

// Modules returns the registered modules in order.
func (r *Registry) Modules() []Module { return r.mods }

// Bind registers every module's flags into fs.
func (r *Registry) Bind(fs *flag.FlagSet) {
	for _, m := range r.mods {
		m.Bind(fs)
	}
}

// FlagNames returns the set of all flag names declared across descriptors.
func (r *Registry) FlagNames() map[string]bool {
	out := map[string]bool{}
	for _, m := range r.mods {
		for _, f := range m.Descriptor().Flags {
			for _, n := range f.Names {
				out[n] = true
			}
		}
	}
	return out
}

const flagCol = 28 // column where flag usage text starts

// flagLabel renders "-k, --key <vless://...>".
func flagLabel(f FlagSpec) string {
	parts := make([]string, 0, len(f.Names))
	for _, n := range f.Names {
		if len(n) == 1 {
			parts = append(parts, "-"+n)
		} else {
			parts = append(parts, "--"+n)
		}
	}
	label := strings.Join(parts, ", ")
	if f.Placeholder != "" {
		label += " " + f.Placeholder
	}
	return label
}

func writeFlag(b *strings.Builder, f FlagSpec) {
	left := "  " + flagLabel(f)
	usage := f.Usage
	if f.Repeatable {
		usage += " (повторяемый)"
	}
	if f.Default != "" {
		usage += " (по умолчанию: " + f.Default + ")"
	}
	if usage == "" {
		b.WriteString(left + "\n")
		return
	}
	if len(left)+2 > flagCol {
		b.WriteString(left + "\n" + strings.Repeat(" ", flagCol) + usage + "\n")
		return
	}
	b.WriteString(left + strings.Repeat(" ", flagCol-len(left)) + usage + "\n")
}

// Help writes the generated usage text.
func (r *Registry) Help(w io.Writer) {
	var b strings.Builder
	if r.tagline != "" {
		b.WriteString(r.name + " — " + r.tagline + "\n\n")
	}
	b.WriteString("Usage:\n  " + r.synopsis + "\n")
	for _, m := range r.mods {
		d := m.Descriptor()
		if len(d.Flags) == 0 {
			continue
		}
		head := d.Title
		if d.Summary != "" {
			head += " — " + d.Summary
		}
		b.WriteString("\n" + head + "\n")
		for _, f := range d.Flags {
			writeFlag(&b, f)
		}
	}
	if env := r.envSection(); env != "" {
		b.WriteString("\nEnvironment:\n" + env)
	}
	_, _ = io.WriteString(w, b.String())
}

// envSection lists the environment variables backing flags, in module order.
func (r *Registry) envSection() string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, m := range r.mods {
		for _, f := range m.Descriptor().Flags {
			if len(f.Env) == 0 {
				continue
			}
			key := strings.Join(f.Env, ", ")
			if seen[key] {
				continue
			}
			seen[key] = true
			left := "  " + key
			usage := f.Usage
			if len(left)+2 > flagCol {
				b.WriteString(left + "\n" + strings.Repeat(" ", flagCol) + usage + "\n")
			} else {
				b.WriteString(left + strings.Repeat(" ", flagCol-len(left)) + usage + "\n")
			}
		}
	}
	return b.String()
}

// Man writes a roff man page generated from the descriptors. It is offered for
// completeness/parity checks; the shipped man page is the curated singctl.1.
func (r *Registry) Man(w io.Writer) {
	var b strings.Builder
	fmt.Fprintf(&b, ".TH %s 1\n", strings.ToUpper(r.name))
	fmt.Fprintf(&b, ".SH NAME\n%s \\- %s\n", r.name, r.tagline)
	fmt.Fprintf(&b, ".SH SYNOPSIS\n%s\n", r.synopsis)
	for _, m := range r.mods {
		d := m.Descriptor()
		fmt.Fprintf(&b, ".SH %s\n", strings.ToUpper(d.Title))
		if d.Doc != "" {
			fmt.Fprintf(&b, ".PP\n%s\n", d.Doc)
		}
		for _, f := range d.Flags {
			fmt.Fprintf(&b, ".TP\n.B %s\n%s\n", flagLabel(f), f.Usage)
		}
	}
	_, _ = io.WriteString(w, b.String())
}
