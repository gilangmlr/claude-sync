package sync

import "time"

// DefaultAutoPushDebounce is the default minimum interval between automatic
// background pushes triggered by editor hooks.
const DefaultAutoPushDebounce = 5 * time.Minute

// ShouldAutoPush reports whether an automatic push should run now.
//
// force bypasses the debounce window (used by the SessionEnd hook so the final
// state of a session is always uploaded). Otherwise a push is allowed only when
// at least debounce has elapsed since lastAttempt. A zero lastAttempt (no prior
// auto-push recorded) always allows a push. Clock skew where now precedes
// lastAttempt is treated as "inside the window" and suppressed.
func ShouldAutoPush(force bool, lastAttempt, now time.Time, debounce time.Duration) bool {
	if force {
		return true
	}
	if lastAttempt.IsZero() {
		return true
	}
	return now.Sub(lastAttempt) >= debounce
}
