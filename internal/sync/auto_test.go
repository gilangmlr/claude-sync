package sync

import (
	"testing"
	"time"
)

func TestShouldAutoPush(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	debounce := 5 * time.Minute

	tests := []struct {
		name        string
		force       bool
		lastAttempt time.Time
		now         time.Time
		want        bool
	}{
		{
			name:        "force always pushes even inside the window",
			force:       true,
			lastAttempt: base,
			now:         base.Add(1 * time.Second),
			want:        true,
		},
		{
			name:        "no prior attempt pushes",
			force:       false,
			lastAttempt: time.Time{},
			now:         base,
			want:        true,
		},
		{
			name:        "inside the window is suppressed",
			force:       false,
			lastAttempt: base,
			now:         base.Add(debounce - time.Second),
			want:        false,
		},
		{
			name:        "exactly at the window pushes",
			force:       false,
			lastAttempt: base,
			now:         base.Add(debounce),
			want:        true,
		},
		{
			name:        "past the window pushes",
			force:       false,
			lastAttempt: base,
			now:         base.Add(debounce + time.Minute),
			want:        true,
		},
		{
			name:        "clock skew (now before last) is suppressed",
			force:       false,
			lastAttempt: base,
			now:         base.Add(-time.Minute),
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldAutoPush(tt.force, tt.lastAttempt, tt.now, debounce)
			if got != tt.want {
				t.Errorf("ShouldAutoPush(force=%v, last=%v, now=%v, debounce=%v) = %v, want %v",
					tt.force, tt.lastAttempt, tt.now, debounce, got, tt.want)
			}
		})
	}
}

func TestDefaultAutoPushDebounce(t *testing.T) {
	if DefaultAutoPushDebounce != 5*time.Minute {
		t.Errorf("DefaultAutoPushDebounce = %v, want 5m", DefaultAutoPushDebounce)
	}
}
