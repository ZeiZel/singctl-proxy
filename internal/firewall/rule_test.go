package firewall

import "testing"

func TestRule_Validate_OK(t *testing.T) {
	cases := []Rule{
		{ID: "1", Action: ActionBlock, Domain: "example.com"},
		{ID: "2", Action: ActionAllow, Domain: "example.com"},
		{ID: "3", Action: ActionBlock, CIDR: "10.0.0.0/8"},
		{ID: "4", Action: ActionBlock, Process: "codex"},
	}
	for _, r := range cases {
		if err := r.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", r, err)
		}
	}
}

func TestRule_Validate_RejectsUnknownAction(t *testing.T) {
	r := Rule{ID: "1", Action: "deny", Domain: "example.com"}
	if err := r.Validate(); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestRule_Validate_RejectsZeroOrMultipleMatchFields(t *testing.T) {
	cases := []Rule{
		{ID: "1", Action: ActionBlock},                                      // none
		{ID: "2", Action: ActionBlock, Domain: "a.com", CIDR: "1.2.3.4/32"}, // two
	}
	for _, r := range cases {
		if err := r.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", r)
		}
	}
}

func TestRule_Validate_RejectsBadCIDR(t *testing.T) {
	r := Rule{ID: "1", Action: ActionBlock, CIDR: "not-a-cidr"}
	if err := r.Validate(); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestRule_MatchDescription(t *testing.T) {
	r := Rule{Action: ActionBlock, Domain: "example.com"}
	if got, want := r.MatchDescription(), "block domain example.com"; got != want {
		t.Errorf("MatchDescription() = %q, want %q", got, want)
	}
}
