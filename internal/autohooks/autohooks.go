// Package autohooks adds and removes the claude-sync auto-push hooks inside a
// Claude Code settings.json document, operating on the parsed JSON object so the
// rest of the file is preserved. All operations are idempotent.
package autohooks

import "strings"

// Marker identifies a claude-sync auto hook command. Any hook command containing
// this substring is considered ours (for detection and removal).
const Marker = "claude-sync auto"

// Hook commands installed by Ensure. Stop debounces; SessionEnd forces so the
// final session state is always uploaded. Both are guarded so they no-op (not
// error) on a machine where claude-sync isn't installed.
const (
	StopCommand       = "command -v claude-sync >/dev/null 2>&1 && claude-sync auto || true"
	SessionEndCommand = "command -v claude-sync >/dev/null 2>&1 && claude-sync auto --force || true"
)

// Ensure adds the Stop and SessionEnd auto hooks to settings if absent.
// Returns true if settings was modified.
func Ensure(settings map[string]any) bool {
	// Avoid short-circuit so both events are evaluated.
	a := ensureEvent(settings, "Stop", StopCommand)
	b := ensureEvent(settings, "SessionEnd", SessionEndCommand)
	return a || b
}

// Remove deletes any claude-sync auto hooks from settings.
// Returns true if settings was modified.
func Remove(settings map[string]any) bool {
	a := removeEvent(settings, "Stop")
	b := removeEvent(settings, "SessionEnd")
	changed := a || b
	if hooks, ok := settings["hooks"].(map[string]any); ok && len(hooks) == 0 {
		delete(settings, "hooks")
	}
	return changed
}

// Detect reports whether any claude-sync auto hook is present in settings.
func Detect(settings map[string]any) bool {
	return eventHasAuto(eventArray(settings, "Stop")) ||
		eventHasAuto(eventArray(settings, "SessionEnd"))
}

func ensureEvent(settings map[string]any, event, command string) bool {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	arr, _ := hooks[event].([]any)
	if eventHasAuto(arr) {
		return false
	}
	group := map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	}
	hooks[event] = append(arr, group)
	return true
}

func removeEvent(settings map[string]any, event string) bool {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false
	}
	arr, ok := hooks[event].([]any)
	if !ok {
		return false
	}
	kept := make([]any, 0, len(arr))
	for _, g := range arr {
		if groupHasAuto(g) {
			continue
		}
		kept = append(kept, g)
	}
	if len(kept) == len(arr) {
		return false
	}
	if len(kept) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = kept
	}
	return true
}

func eventArray(settings map[string]any, event string) []any {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	arr, _ := hooks[event].([]any)
	return arr
}

func eventHasAuto(arr []any) bool {
	for _, g := range arr {
		if groupHasAuto(g) {
			return true
		}
	}
	return false
}

func groupHasAuto(group any) bool {
	gm, ok := group.(map[string]any)
	if !ok {
		return false
	}
	inner, ok := gm["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, Marker) {
			return true
		}
	}
	return false
}
