// Package policy is the pure decision engine. Given the current mode, the
// debounced Cisco transition, and any user intent, Decide returns the actions
// the executor must perform. No I/O, no state — 100% table-testable.
package policy

// Mode is the runtime mode the policy reasons about.
type Mode int

const (
	ModeProxy     Mode = iota // proxy-only (default)
	ModeVPN                   // forwarder up
	ModeSwitching             // in transition (inert to observations)
)

// CiscoState is the debounced Cisco connection state. Unknown only before the
// first observation has been committed.
type CiscoState int

const (
	CiscoUnknown CiscoState = iota
	CiscoInactive
	CiscoActive
)

// UserIntent is an explicit user request, if any.
type UserIntent int

const (
	IntentNone UserIntent = iota
	IntentEnableVPN
	IntentDisableVPN
)

// Action is a single thing the executor should do, in slice order.
type Action int

const (
	ActNone Action = iota
	ActStartForwarder
	ActStopForwarder
	ActShowWarning
	ActRefreshProxy
	ActFailClosed // immediately stop forwarding (drop) before tearing down TUN
	ActNotify
	// ActBindPhysical is retained for explicit runtime callers. Automatic
	// coexistence never emits it: proxy-only egress follows the system routes.
	ActBindPhysical
	// ActUnbindProxy releases the physical bind and re-dials the proxy over the
	// restored default route once Cisco disconnects.
	ActUnbindProxy
)

// DecideInput is the full input to a decision.
type DecideInput struct {
	Mode             Mode
	PrevCisco        CiscoState
	NewCisco         CiscoState
	Intent           UserIntent
	PhysIfaceChanged bool
	// ProxyBoundPhys reports a stale physical bind in proxy-only mode, which
	// must be released even if no Cisco transition was observed.
	ProxyBoundPhys bool
	// CiscoOwnsDefault is true only when Cisco holds the unscoped default route
	// (full-tunnel). Informational now (surfaced in diagnostics): we no longer
	// bind the proxy to the physical NIC in any case — riding the default route
	// is correct for no-Cisco, split-tunnel, and (measured) full-tunnel Cisco,
	// whose tunnel reaches the internet while non-tunnel egress is blocked.
	CiscoOwnsDefault bool
}

// DecisionResult is the decision output.
type DecisionResult struct {
	Actions  []Action
	NextMode Mode
}

func only(mode Mode, acts ...Action) DecisionResult {
	if len(acts) == 0 {
		acts = []Action{ActNone}
	}
	return DecisionResult{Actions: acts, NextMode: mode}
}

// Decide computes the actions and next mode. User intent takes precedence over
// observation-driven transitions. Proxy refreshes require a concrete Cisco
// edge. Releasing a competing TUN is level-triggered, including at startup.
func Decide(in DecideInput) DecisionResult {
	// 1. Explicit user intent.
	switch in.Intent {
	case IntentEnableVPN:
		// Rule (a): never enter VPN while Cisco is active.
		if in.NewCisco == CiscoActive {
			return only(in.Mode, ActShowWarning)
		}
		if in.Mode == ModeVPN {
			return only(ModeVPN, ActNone)
		}
		return only(ModeVPN, ActStartForwarder)
	case IntentDisableVPN:
		if in.Mode == ModeVPN {
			return only(ModeProxy, ActStopForwarder)
		}
		return only(in.Mode, ActNone)
	}

	// 2. Observation-driven transitions (Intent == None).
	if in.Mode == ModeSwitching {
		return only(ModeSwitching, ActNone) // inert mid-transition
	}

	ciscoChanged := (in.PrevCisco == CiscoInactive && in.NewCisco == CiscoActive) ||
		(in.PrevCisco == CiscoActive && in.NewCisco == CiscoInactive)

	switch {
	case in.NewCisco == CiscoActive && in.Mode == ModeVPN:
		// Rule (b): Cisco appeared while we hold the tunnel — fail closed and
		// drop our VPN immediately so not a single packet rides our tunnel.
		return only(ModeProxy, ActFailClosed, ActStopForwarder, ActNotify)
	case in.Mode == ModeProxy && in.ProxyBoundPhys:
		// Rule (c): proxy-only coexistence NO LONGER binds the egress to the
		// physical NIC. Measured on a real corporate full-tunnel Cisco: the
		// TUNNEL reaches the internet (so the proxy works by riding the default
		// route), while non-tunnel (en0) egress is firewall-blocked — so binding
		// to en0 BREAKS the proxy instead of helping. Split-tunnel already rides
		// en0. In every observed case riding the default route is correct, so we
		// never bind; if we somehow hold a stale bind, release it here.
		return only(ModeProxy, ActUnbindProxy)
	case in.Mode == ModeProxy && ciscoChanged:
		// Existing upstream connections (including multiplexed transports) may
		// still belong to the old network. Recreate them after the debounced
		// transition, without binding to a NIC or changing any VPN routes.
		return only(ModeProxy, ActRefreshProxy)
	case in.PhysIfaceChanged && in.Mode == ModeVPN:
		// Physical interface changed under our TUN — rebind the proxy.
		return only(ModeVPN, ActRefreshProxy)
	default:
		return only(in.Mode, ActNone)
	}
}
