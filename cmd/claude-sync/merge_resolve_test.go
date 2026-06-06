package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gilangmlr/claude-sync/internal/sync"
)

func TestMergeConflictFile(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "history.jsonl")
	conflict := orig + ".conflict.20260101-000000"

	localData := `{"timestamp":"2026-01-01T00:00:02Z","v":"b"}` + "\n"
	remoteData := `{"timestamp":"2026-01-01T00:00:01Z","v":"a"}` + "\n"
	if err := os.WriteFile(orig, []byte(localData), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict, []byte(remoteData), 0644); err != nil {
		t.Fatal(err)
	}

	state, err := sync.LoadStateFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := conflictFile{OriginalPath: orig, ConflictPath: conflict}
	if err := mergeConflictFile(c, dir, state); err != nil {
		t.Fatalf("mergeConflictFile: %v", err)
	}

	got, _ := os.ReadFile(orig)
	want := `{"timestamp":"2026-01-01T00:00:01Z","v":"a"}` + "\n" + `{"timestamp":"2026-01-01T00:00:02Z","v":"b"}` + "\n"
	if string(got) != want {
		t.Errorf("merged content\n got: %q\nwant: %q", got, want)
	}
	if _, err := os.Stat(conflict); !os.IsNotExist(err) {
		t.Errorf("conflict sidecar should be deleted, stat err = %v", err)
	}
}

func TestIsJSONLPath(t *testing.T) {
	if !isJSONLPath("/a/b/history.jsonl") {
		t.Error("want true for .jsonl")
	}
	if isJSONLPath("/a/b/settings.json") {
		t.Error("want false for .json")
	}
}
