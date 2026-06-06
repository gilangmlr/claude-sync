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

	// Seed a prior base entry with a different hash so DetectChanges can compare
	// against it — simulating the last-synced state before the conflict was created.
	info, err := os.Stat(orig)
	if err != nil {
		t.Fatal(err)
	}
	state.UpdateFile("history.jsonl", info, "000000basehash")

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

	// After resolution the state still holds the old base hash, so DetectChanges
	// must report history.jsonl as a pending upload (action "modify").
	changes, err := state.DetectChanges(dir, []string{"history.jsonl"})
	if err != nil {
		t.Fatalf("DetectChanges: %v", err)
	}
	found := false
	for _, ch := range changes {
		if ch.Path == "history.jsonl" && (ch.Action == "modify" || ch.Action == "add") {
			found = true
			break
		}
	}
	if !found {
		t.Error("resolved file should be reported as a pending change (modify/add) so next push uploads it")
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

func TestBatchResolveKeepMerge(t *testing.T) {
	dir := t.TempDir()
	state, err := sync.LoadStateFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	// A .jsonl conflict (should merge) and a .txt conflict (should be skipped).
	jl := filepath.Join(dir, "history.jsonl")
	jlConf := jl + ".conflict.20260101-000000"
	if err := os.WriteFile(jl, []byte(`{"timestamp":"2026-01-01T00:00:02Z","v":"b"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jlConf, []byte(`{"timestamp":"2026-01-01T00:00:01Z","v":"a"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(dir, "notes.txt")
	txtConf := txt + ".conflict.20260101-000000"
	if err := os.WriteFile(txt, []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(txtConf, []byte("remote\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Seed a prior base entry for the .jsonl file so DetectChanges has a hash
	// to compare against after the merge resolves it.
	jlInfo, err := os.Stat(jl)
	if err != nil {
		t.Fatal(err)
	}
	state.UpdateFile("history.jsonl", jlInfo, "000000basehash")

	conflicts := []conflictFile{
		{OriginalPath: jl, ConflictPath: jlConf},
		{OriginalPath: txt, ConflictPath: txtConf},
	}
	if err := batchResolveConflicts(conflicts, "merge", dir, state); err != nil {
		t.Fatalf("batchResolveConflicts: %v", err)
	}

	// .jsonl merged + sidecar gone.
	got, _ := os.ReadFile(jl)
	want := `{"timestamp":"2026-01-01T00:00:01Z","v":"a"}` + "\n" + `{"timestamp":"2026-01-01T00:00:02Z","v":"b"}` + "\n"
	if string(got) != want {
		t.Errorf("jsonl not merged\n got: %q\nwant: %q", got, want)
	}
	if _, err := os.Stat(jlConf); !os.IsNotExist(err) {
		t.Errorf("jsonl sidecar should be gone")
	}
	// .txt left untouched (skipped).
	if _, err := os.Stat(txtConf); err != nil {
		t.Errorf("txt sidecar should remain, got %v", err)
	}

	// After batch merge the state still holds the old base hash, so DetectChanges
	// must report history.jsonl as a pending upload (action "modify").
	changes, err := state.DetectChanges(dir, []string{"history.jsonl"})
	if err != nil {
		t.Fatalf("DetectChanges: %v", err)
	}
	found := false
	for _, ch := range changes {
		if ch.Path == "history.jsonl" && (ch.Action == "modify" || ch.Action == "add") {
			found = true
			break
		}
	}
	if !found {
		t.Error("merged .jsonl should be reported as a pending change (modify/add) so next push uploads it")
	}
}
