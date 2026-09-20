// Package firewall is the minimal per-destination/per-process firewall from
// docs/v2-spec.md's F6 item 5: a small, engine-agnostic rule set (block or
// allow, matched by destination domain, destination CIDR, or source process
// name), persisted the same way the rest of singctl's user state is (see
// internal/profile's LoadFirewallRules/SaveFirewallRules) and rendered into
// the generated sing-box config as route rules by internal/singbox — the
// same split internal/protocol uses for RenderNode/Renderer: this package
// owns the rule, the engine that consumes it owns the translation.
package firewall

import (
	"fmt"
	"net"
)

// Action is what a Rule does to matching traffic.
type Action string

const (
	// ActionBlock drops matching traffic (rendered as a route rule pointing
	// at the generated config's "block" outbound).
	ActionBlock Action = "block"
	// ActionAllow explicitly bypasses the tunnel for matching traffic
	// (rendered as a route rule pointing at "direct" — the same outbound
	// already used for private-IP and .ru-domain traffic), so a user can
	// force one destination or process off the proxy without touching
	// everything else.
	ActionAllow Action = "allow"
)

// Rule is one firewall rule. Exactly one of Domain, CIDR, or Process must be
// set — a rule matches by destination domain (and its subdomains),
// destination CIDR, or source process name (as Clash/sing-box's
// metadata.process reports it), never more than one at a time.
type Rule struct {
	ID      string `json:"id"`
	Action  Action `json:"action"`
	Domain  string `json:"domain,omitempty"`
	CIDR    string `json:"cidr,omitempty"`
	Process string `json:"process,omitempty"`
}

// Validate reports whether r is well-formed: a known Action, a valid CIDR
// when Domain/CIDR/Process is given, and exactly one match field set.
func (r Rule) Validate() error {
	switch r.Action {
	case ActionBlock, ActionAllow:
	default:
		return fmt.Errorf("firewall: unknown action %q (want %q or %q)", r.Action, ActionBlock, ActionAllow)
	}
	n := 0
	if r.Domain != "" {
		n++
	}
	if r.CIDR != "" {
		n++
	}
	if r.Process != "" {
		n++
	}
	if n != 1 {
		return fmt.Errorf("firewall: rule must match exactly one of domain, cidr, or process (got %d)", n)
	}
	if r.CIDR != "" {
		if _, _, err := net.ParseCIDR(r.CIDR); err != nil {
			return fmt.Errorf("firewall: invalid cidr %q: %w", r.CIDR, err)
		}
	}
	return nil
}

// MatchDescription renders a short human summary of what r matches, e.g.
// "block domain example.com" — used by clients that want a one-line label
// without duplicating the field-precedence logic.
func (r Rule) MatchDescription() string {
	switch {
	case r.Domain != "":
		return fmt.Sprintf("%s domain %s", r.Action, r.Domain)
	case r.CIDR != "":
		return fmt.Sprintf("%s cidr %s", r.Action, r.CIDR)
	case r.Process != "":
		return fmt.Sprintf("%s process %s", r.Action, r.Process)
	default:
		return string(r.Action)
	}
}
