package sync

import "testing"

func TestMergeLines(t *testing.T) {
	tests := []struct {
		name          string
		local, remote string
		want          string
	}{
		{"append net-new", "A\nB\nC\n", "A\nB\nD\n", "A\nB\nC\nD\n"},
		{"empty local takes remote", "", "X\nY\n", "X\nY\n"},
		{"empty remote keeps local", "Y\nZ\n", "", "Y\nZ\n"},
		{"identical is unchanged", "A\nB", "A\nB", "A\nB"},
		{"dedupes remote internal duplicates", "A\n", "B\nB\n", "A\nB\n"},
		{"no trailing newline local, add", "A\nB", "C\n", "A\nB\nC\n"},
		{"remote subset of local", "A\nB\nC\n", "B\nC\n", "A\nB\nC\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(MergeLines([]byte(tt.local), []byte(tt.remote)))
			if got != tt.want {
				t.Errorf("MergeLines(%q, %q) = %q, want %q", tt.local, tt.remote, got, tt.want)
			}
		})
	}
}

func TestMergeForPull(t *testing.T) {
	t.Run("jsonl is merged", func(t *testing.T) {
		local := []byte(`{"ts":1,"v":"a"}` + "\n")
		remote := []byte(`{"ts":2,"v":"b"}` + "\n")
		merged, ok, err := MergeForPull("history.jsonl", local, remote)
		if err != nil || !ok {
			t.Fatalf("expected merged jsonl, ok=%v err=%v", ok, err)
		}
		if len(merged) == 0 {
			t.Error("merged jsonl is empty")
		}
	})

	t.Run("structured json is not merged", func(t *testing.T) {
		_, ok, err := MergeForPull("settings.json", []byte(`{"a":1}`), []byte(`{"b":2}`))
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ok {
			t.Error("settings.json must NOT be line-merged (would corrupt JSON)")
		}
	})

	t.Run("markdown is line-merged", func(t *testing.T) {
		merged, ok, err := MergeForPull("memory/foo.md", []byte("# A\nlocal\n"), []byte("# A\nremote\n"))
		if err != nil || !ok {
			t.Fatalf("expected merged md, ok=%v err=%v", ok, err)
		}
		if string(merged) != "# A\nlocal\nremote\n" {
			t.Errorf("got %q", string(merged))
		}
	})
}
