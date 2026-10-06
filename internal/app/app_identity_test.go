package app

import "testing"

func TestResolveApplicationPIDIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		beforeOK, afterOK   bool
		wantID              string
		verified            bool
	}{
		{"stable", "pid:start1", "pid:start1", true, true, "com.app", true},
		{"reused", "pid:start1", "pid:start2", true, true, "", false},
		{"exited", "pid:start1", "", true, false, "", false},
		{"unverifiable", "", "", false, false, "com.app", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			resolved := false
			id, token, verified := resolveApplicationPID(7, func(int) string {
				if calls != 1 {
					t.Fatal("resolution not bracketed")
				}
				resolved = true
				return "com.app"
			}, func(int) (string, bool) {
				calls++
				if calls == 1 {
					return tc.before, tc.beforeOK
				}
				if !resolved {
					t.Fatal("second identity before bundle lookup")
				}
				return tc.after, tc.afterOK
			})
			if calls != 2 || id != tc.wantID || verified != tc.verified || (verified && token != tc.before) {
				t.Fatalf("id=%q token=%q verified=%v calls=%d", id, token, verified, calls)
			}
		})
	}
}
