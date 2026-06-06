package autohooks

import (
	"encoding/json"
	"testing"
)

func parse(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return m
}

func TestEnsureOnEmpty(t *testing.T) {
	s := map[string]any{}
	if !Ensure(s) {
		t.Fatal("Ensure should report change on empty settings")
	}
	if !Detect(s) {
		t.Fatal("Detect should be true after Ensure")
	}
	// Both events present.
	for _, ev := range []string{"Stop", "SessionEnd"} {
		if !eventHasAuto(eventArray(s, ev)) {
			t.Errorf("%s should contain an auto hook", ev)
		}
	}
}

func TestEnsureIdempotent(t *testing.T) {
	s := map[string]any{}
	Ensure(s)
	if Ensure(s) {
		t.Fatal("second Ensure should report no change")
	}
}

func TestEnsurePreservesExistingHooks(t *testing.T) {
	s := parse(t, `{
		"permissions": {"defaultMode": "auto"},
		"hooks": {
			"Stop": [
				{"hooks": [{"type": "command", "command": "echo existing"}]}
			]
		}
	}`)
	if !Ensure(s) {
		t.Fatal("Ensure should add to existing hooks")
	}
	stop := eventArray(s, "Stop")
	if len(stop) != 2 {
		t.Fatalf("Stop should have 2 groups (existing + auto), got %d", len(stop))
	}
	// The non-auto hook must survive.
	if Remove(s); Detect(s) {
		t.Fatal("Detect should be false after Remove")
	}
	stop = eventArray(s, "Stop")
	if len(stop) != 1 {
		t.Fatalf("after Remove, Stop should keep the 1 existing group, got %d", len(stop))
	}
	if _, ok := s["permissions"]; !ok {
		t.Fatal("Remove must not touch unrelated keys")
	}
}

func TestRemoveOnAbsentIsNoop(t *testing.T) {
	s := map[string]any{"foo": "bar"}
	if Remove(s) {
		t.Fatal("Remove should report no change when no hooks present")
	}
}

func TestRemoveDeletesEmptyHooksKey(t *testing.T) {
	s := map[string]any{}
	Ensure(s)
	Remove(s)
	if _, ok := s["hooks"]; ok {
		t.Fatal("hooks key should be deleted once empty")
	}
}

func TestDetectFalseOnUnrelatedHook(t *testing.T) {
	s := parse(t, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`)
	if Detect(s) {
		t.Fatal("Detect should be false for unrelated hooks")
	}
}
