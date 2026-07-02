package license

import (
	"time"

	"singctl/internal/profile"
)

// Decision is the outcome of DecideEnforcement: whether to allow startup to
// proceed, why not when it doesn't, an optional non-fatal warning, and the
// license state to persist.
type Decision struct {
	Allow  bool
	Reason string // non-empty when !Allow: the fatal message to show the user
	Warn   string // non-empty: a non-fatal warning to print even when allowed
	State  profile.LicenseState
}

// Messages are centralized here so the CLI and GUI show identical text.
const (
	msgFirstActivationOffline = "Для первой активации лицензии нужен доступ к серверу. " +
		"Подключитесь к интернету и запустите снова."
	msgNotFoundOnServer = "лицензия не найдена на сервере"
	msgRevoked          = "лицензия отозвана — обратитесь к поставщику"
	msgExpiredOnServer  = "срок лицензии истёк"
	msgUnknownAfterOnce = "не удалось подтвердить статус лицензии на сервере (ответ неизвестен) — продолжаем работу"
)

// DecideEnforcement is the pure state machine behind the license activation
// gate: given the persisted LicenseState, the result of asking the server
// (status, fetchErr), and the current time, it decides whether startup may
// proceed and what LicenseState to persist afterwards. It has no I/O so it is
// fully unit-testable; enforceLicense (CLI) and the GUI's activation gate are
// thin shells that fetch status and persist State.
//
// Rules:
//   - Never activated (State.ActivatedOnce == false): the server MUST be
//     reachable and report "active" for startup to proceed — this is the
//     "contact the server successfully at least once" requirement. Any other
//     outcome (network error, revoked, expired, unknown) blocks.
//   - Activated at least once: a network error means "offline" and startup
//     proceeds using the previously persisted state (indefinite offline use).
//     A reachable server reporting "revoked" or "expired" blocks; "active"
//     keeps going; "unknown" (e.g. the id was purged) is treated as
//     inconclusive and only warns, so a flaky/misconfigured server can't brick
//     an already-activated install.
//
// Whenever the server was actually reached (fetchErr == nil), State.LastStatus
// and State.LastCheckUnix are updated to reflect what it said, whether or not
// that allows startup — so a subsequent offline run and any status display
// (e.g. the GUI) can see the last known verdict.
func DecideEnforcement(state profile.LicenseState, status Status, fetchErr error, now time.Time) Decision {
	if !state.ActivatedOnce {
		if fetchErr != nil {
			return Decision{Allow: false, Reason: msgFirstActivationOffline, State: state}
		}
		newState := profile.LicenseState{
			ActivatedOnce: status == StatusActive,
			LastCheckUnix: now.Unix(),
			LastStatus:    string(status),
		}
		switch status {
		case StatusActive:
			return Decision{Allow: true, State: newState}
		case StatusRevoked:
			return Decision{Allow: false, Reason: msgRevoked, State: newState}
		case StatusExpired:
			return Decision{Allow: false, Reason: msgExpiredOnServer, State: newState}
		default: // StatusUnknown or anything unrecognized
			return Decision{Allow: false, Reason: msgNotFoundOnServer, State: newState}
		}
	}

	// Already activated at least once: an unreachable server never blocks.
	if fetchErr != nil {
		return Decision{Allow: true, State: state}
	}
	newState := profile.LicenseState{
		ActivatedOnce: true,
		LastCheckUnix: now.Unix(),
		LastStatus:    string(status),
	}
	switch status {
	case StatusActive:
		return Decision{Allow: true, State: newState}
	case StatusRevoked:
		return Decision{Allow: false, Reason: msgRevoked, State: newState}
	case StatusExpired:
		return Decision{Allow: false, Reason: msgExpiredOnServer, State: newState}
	default: // StatusUnknown
		return Decision{Allow: true, Warn: msgUnknownAfterOnce, State: newState}
	}
}
