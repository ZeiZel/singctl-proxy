package sub

import (
	"testing"
	"time"
)

func TestSubscription_Interval(t *testing.T) {
	tests := []struct {
		name string
		sub  Subscription
		want time.Duration
	}{
		{
			name: "panel-reported interval wins",
			sub:  Subscription{Meta: Meta{UpdateInterval: 6 * time.Hour}},
			want: 6 * time.Hour,
		},
		{
			name: "zero interval falls back to the default",
			sub:  Subscription{Meta: Meta{}},
			want: DefaultInterval,
		},
		{
			name: "negative interval (should never happen, but must not be trusted) falls back too",
			sub:  Subscription{Meta: Meta{UpdateInterval: -time.Hour}},
			want: DefaultInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sub.Interval(); got != tt.want {
				t.Errorf("Interval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSubscription_DueAt(t *testing.T) {
	t.Run("never fetched is due immediately (zero time)", func(t *testing.T) {
		s := Subscription{URL: "https://panel.example/sub/abc"}
		if got := s.DueAt(); !got.IsZero() {
			t.Errorf("DueAt() = %v, want zero time", got)
		}
	})

	t.Run("due at LastUpdate plus the effective interval", func(t *testing.T) {
		last := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		s := Subscription{
			LastUpdate: last,
			Meta:       Meta{UpdateInterval: 3 * time.Hour},
		}
		want := last.Add(3 * time.Hour)
		if got := s.DueAt(); !got.Equal(want) {
			t.Errorf("DueAt() = %v, want %v", got, want)
		}
	})

	t.Run("due at LastUpdate plus the default interval when panel reported none", func(t *testing.T) {
		last := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		s := Subscription{LastUpdate: last}
		want := last.Add(DefaultInterval)
		if got := s.DueAt(); !got.Equal(want) {
			t.Errorf("DueAt() = %v, want %v", got, want)
		}
	})
}

func TestSubscription_Label(t *testing.T) {
	tests := []struct {
		name string
		sub  Subscription
		want string
	}{
		{
			name: "panel title wins when present",
			sub:  Subscription{Title: "My VIP Plan", URL: "https://panel.example/sub/abc123"},
			want: "My VIP Plan",
		},
		{
			name: "falls back to the URL host when no title",
			sub:  Subscription{URL: "https://panel.example.com/sub/abc123?token=xyz"},
			want: "panel.example.com",
		},
		{
			name: "falls back to the host, dropping any port",
			sub:  Subscription{URL: "https://panel.example.com:8443/sub/abc123"},
			want: "panel.example.com:8443",
		},
		{
			name: "url with no scheme separator falls back to the raw url verbatim",
			sub:  Subscription{URL: "not-a-url-at-all"},
			want: "not-a-url-at-all",
		},
		{
			name: "empty title and empty url",
			sub:  Subscription{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sub.Label(); got != tt.want {
				t.Errorf("Label() = %q, want %q", got, tt.want)
			}
		})
	}
}
