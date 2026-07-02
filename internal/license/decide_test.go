package license

import (
	"errors"
	"testing"
	"time"

	"singctl/internal/profile"
)

func TestDecideEnforcement(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	netErr := errors.New("dial tcp: connection refused")

	cases := []struct {
		name      string
		state     profile.LicenseState
		status    Status
		fetchErr  error
		wantAllow bool
		wantWarn  bool
		wantState profile.LicenseState
	}{
		{
			name:      "first activation: server unreachable blocks",
			state:     profile.LicenseState{},
			fetchErr:  netErr,
			wantAllow: false,
			wantState: profile.LicenseState{}, // unchanged: nothing learned
		},
		{
			name:      "first activation: active allows and records it",
			state:     profile.LicenseState{},
			status:    StatusActive,
			wantAllow: true,
			wantState: profile.LicenseState{ActivatedOnce: true, LastCheckUnix: now.Unix(), LastStatus: "active"},
		},
		{
			name:      "first activation: revoked blocks, not recorded as activated",
			state:     profile.LicenseState{},
			status:    StatusRevoked,
			wantAllow: false,
			wantState: profile.LicenseState{ActivatedOnce: false, LastCheckUnix: now.Unix(), LastStatus: "revoked"},
		},
		{
			name:      "first activation: expired blocks",
			state:     profile.LicenseState{},
			status:    StatusExpired,
			wantAllow: false,
			wantState: profile.LicenseState{ActivatedOnce: false, LastCheckUnix: now.Unix(), LastStatus: "expired"},
		},
		{
			name:      "first activation: unknown blocks",
			state:     profile.LicenseState{},
			status:    StatusUnknown,
			wantAllow: false,
			wantState: profile.LicenseState{ActivatedOnce: false, LastCheckUnix: now.Unix(), LastStatus: "unknown"},
		},
		{
			name:      "activated: server unreachable allows offline, state untouched",
			state:     profile.LicenseState{ActivatedOnce: true, LastCheckUnix: 1, LastStatus: "active"},
			fetchErr:  netErr,
			wantAllow: true,
			wantState: profile.LicenseState{ActivatedOnce: true, LastCheckUnix: 1, LastStatus: "active"},
		},
		{
			name:      "activated: active refreshes state",
			state:     profile.LicenseState{ActivatedOnce: true, LastCheckUnix: 1, LastStatus: "active"},
			status:    StatusActive,
			wantAllow: true,
			wantState: profile.LicenseState{ActivatedOnce: true, LastCheckUnix: now.Unix(), LastStatus: "active"},
		},
		{
			name:      "activated: revoked blocks",
			state:     profile.LicenseState{ActivatedOnce: true, LastCheckUnix: 1, LastStatus: "active"},
			status:    StatusRevoked,
			wantAllow: false,
			wantState: profile.LicenseState{ActivatedOnce: true, LastCheckUnix: now.Unix(), LastStatus: "revoked"},
		},
		{
			name:      "activated: expired blocks",
			state:     profile.LicenseState{ActivatedOnce: true, LastCheckUnix: 1, LastStatus: "active"},
			status:    StatusExpired,
			wantAllow: false,
			wantState: profile.LicenseState{ActivatedOnce: true, LastCheckUnix: now.Unix(), LastStatus: "expired"},
		},
		{
			name:      "activated: unknown warns but allows",
			state:     profile.LicenseState{ActivatedOnce: true, LastCheckUnix: 1, LastStatus: "active"},
			status:    StatusUnknown,
			wantAllow: true,
			wantWarn:  true,
			wantState: profile.LicenseState{ActivatedOnce: true, LastCheckUnix: now.Unix(), LastStatus: "unknown"},
		},
		{
			name:      "activated: superseded blocks (activated elsewhere)",
			state:     profile.LicenseState{ActivatedOnce: true, LastCheckUnix: 1, LastStatus: "active"},
			status:    StatusSuperseded,
			wantAllow: false,
			wantState: profile.LicenseState{ActivatedOnce: true, LastCheckUnix: now.Unix(), LastStatus: "superseded"},
		},
		{
			name:      "first activation: superseded blocks",
			state:     profile.LicenseState{},
			status:    StatusSuperseded,
			wantAllow: false,
			wantState: profile.LicenseState{ActivatedOnce: false, LastCheckUnix: now.Unix(), LastStatus: "superseded"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideEnforcement(tc.state, tc.status, tc.fetchErr, now)
			if got.Allow != tc.wantAllow {
				t.Errorf("Allow = %v, want %v (got %+v)", got.Allow, tc.wantAllow, got)
			}
			if !tc.wantAllow && got.Reason == "" {
				t.Error("blocked decision must carry a Reason")
			}
			if tc.wantAllow && got.Reason != "" {
				t.Errorf("allowed decision should not carry a Reason, got %q", got.Reason)
			}
			if tc.wantWarn && got.Warn == "" {
				t.Error("expected a non-empty Warn")
			}
			if !tc.wantWarn && got.Warn != "" {
				t.Errorf("unexpected Warn: %q", got.Warn)
			}
			if got.State != tc.wantState {
				t.Errorf("State = %+v, want %+v", got.State, tc.wantState)
			}
		})
	}
}
