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
)

// DecideInput is the full input to a decision.
type DecideInput struct {
	Mode             Mode
	PrevCisco        CiscoState
	NewCisco         CiscoState
	Intent           UserIntent
	PhysIfaceChanged bool
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
// observation-driven transitions. Cisco transitions are only acted on between
// the concrete Inactive<->Active states; anything involving CiscoUnknown (the
// first observation) is inert so there is no spurious action at startup.
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

	ciscoUp := in.PrevCisco == CiscoInactive && in.NewCisco == CiscoActive
	ciscoDown := in.PrevCisco == CiscoActive && in.NewCisco == CiscoInactive

	switch {
	case ciscoUp && in.Mode == ModeVPN:
		// Rule (b): Cisco appeared while we hold the tunnel — fail closed and
		// drop our VPN immediately so not a single packet rides our tunnel.
		return only(ModeProxy, ActFailClosed, ActStopForwarder, ActNotify)
	case ciscoDown && in.Mode == ModeProxy:
		// Rule (c): Cisco left — re-dial the proxy upstream so apps recover.
		return only(ModeProxy, ActRefreshProxy)
	case in.PhysIfaceChanged && in.Mode == ModeVPN:
		// Physical interface changed under our TUN — rebind the proxy.
		return only(ModeVPN, ActRefreshProxy)
	default:
		return only(in.Mode, ActNone)
	}
}
