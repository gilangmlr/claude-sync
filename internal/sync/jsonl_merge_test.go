package sync

import "testing"

// line builds a compact JSON object line with a timestamp + marker value.
func line(ts, v string) string {
	if ts == "" {
		return `{"v":"` + v + `"}`
	}
	return `{"timestamp":` + ts + `,"v":"` + v + `"}`
}

func TestMergeJSONL(t *testing.T) {
	q := func(s string) string { return `"` + s + `"` } // quote an ISO ts

	cases := []struct {
		name          string
		local, remote string
		want          string
	}{
		{
			name:   "exact-line dedupe drops remote duplicate",
			local:  line(q("2026-01-01T00:00:01Z"), "a") + "\n" + line(q("2026-01-01T00:00:02Z"), "b") + "\n",
			remote: line(q("2026-01-01T00:00:02Z"), "b") + "\n" + line(q("2026-01-01T00:00:03Z"), "c") + "\n",
			want:   line(q("2026-01-01T00:00:01Z"), "a") + "\n" + line(q("2026-01-01T00:00:02Z"), "b") + "\n" + line(q("2026-01-01T00:00:03Z"), "c") + "\n",
		},
		{
			name:   "iso timestamps sort ascending across sides",
			local:  line(q("2026-01-01T00:00:03Z"), "c") + "\n",
			remote: line(q("2026-01-01T00:00:01Z"), "a") + "\n",
			want:   line(q("2026-01-01T00:00:01Z"), "a") + "\n" + line(q("2026-01-01T00:00:03Z"), "c") + "\n",
		},
		{
			name:   "integer epoch ms (history shape) sorts ascending",
			local:  line("1777300268083", "b") + "\n",
			remote: line("1777300268000", "a") + "\n",
			want:   line("1777300268000", "a") + "\n" + line("1777300268083", "b") + "\n",
		},
		{
			name: "epoch seconds detected and normalized vs ms",
			// 1777300268 (10 digits) => seconds => 1777300268000 ms (== remote ms below)
			// 1777300268500 (>=1e12) => ms. So seconds line sorts before the ms line.
			local:  line("1777300268500", "later") + "\n",
			remote: line("1777300268", "earlier") + "\n",
			want:   line("1777300268", "earlier") + "\n" + line("1777300268500", "later") + "\n",
		},
		{
			name: "carry-forward keeps ts-less line adjacent to its predecessor",
			// L1 ts=100ms, L2 no ts (carries 100ms). Remote R ts=50ms.
			local:  line("100000000000000", "L1") + "\n" + line("", "L2") + "\n",
			remote: line("50000000000000", "R") + "\n",
			want:   line("50000000000000", "R") + "\n" + line("100000000000000", "L1") + "\n" + line("", "L2") + "\n",
		},
		{
			name:   "leading ts-less line sorts to front",
			local:  line("", "header") + "\n" + line("100000000000000", "E") + "\n",
			remote: line("50000000000000", "R") + "\n",
			want:   line("", "header") + "\n" + line("50000000000000", "R") + "\n" + line("100000000000000", "E") + "\n",
		},
		{
			name:   "equal timestamps keep local before remote (stable)",
			local:  line("100000000000000", "local") + "\n",
			remote: line("100000000000000", "remote") + "\n",
			want:   line("100000000000000", "local") + "\n" + line("100000000000000", "remote") + "\n",
		},
		{
			name:   "local empty returns remote deduped+sorted",
			local:  "",
			remote: line(q("2026-01-01T00:00:02Z"), "b") + "\n" + line(q("2026-01-01T00:00:01Z"), "a") + "\n",
			want:   line(q("2026-01-01T00:00:01Z"), "a") + "\n" + line(q("2026-01-01T00:00:02Z"), "b") + "\n",
		},
		{
			name:   "crlf normalized and deduped against lf",
			local:  line("100000000000000", "a") + "\r\n",
			remote: line("100000000000000", "a") + "\n",
			want:   line("100000000000000", "a") + "\n",
		},
		{
			name:   "blank lines skipped",
			local:  "\n" + line("100000000000000", "a") + "\n\n",
			remote: "",
			want:   line("100000000000000", "a") + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeJSONL([]byte(tc.local), []byte(tc.remote))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("MergeJSONL mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestMergeJSONLEmptyBoth(t *testing.T) {
	got, err := MergeJSONL(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty output, got %q", got)
	}
}

func TestMergeJSONLInvalidJSON(t *testing.T) {
	if _, err := MergeJSONL([]byte("not json\n"), nil); err == nil {
		t.Fatal("expected error for non-JSON local line")
	}
	if _, err := MergeJSONL(nil, []byte("{bad}\n")); err == nil {
		t.Fatal("expected error for non-JSON remote line")
	}
}

func TestMergeJSONLIdempotent(t *testing.T) {
	in := `{"timestamp":"2026-01-01T00:00:02Z","v":"b"}` + "\n" + `{"timestamp":"2026-01-01T00:00:01Z","v":"a"}` + "\n"
	once, err := MergeJSONL([]byte(in), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	twice, err := MergeJSONL(once, once)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(once) != string(twice) {
		t.Errorf("not idempotent\n once: %q\ntwice: %q", once, twice)
	}
}
