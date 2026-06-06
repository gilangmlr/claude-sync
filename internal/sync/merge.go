package sync

import "strings"

// MergeForPull merges remote into local for a file encountered during pull when
// both sides have diverged. It returns (merged, true) when the file type can be
// safely line-merged, or (nil, false) when the caller should fall back to the
// keep-local + .conflict sidecar path.
//
// Strategy by file type:
//   - .jsonl            timestamp-sorted, exact-line-deduped union (MergeJSONL)
//   - structured .json  NOT merged (line-union would produce invalid JSON) -> sidecar
//   - everything else   plain line union, preserving local and appending
//     net-new remote lines (MergeLines)
func MergeForPull(path string, local, remote []byte) ([]byte, bool, error) {
	switch {
	case isJSONLName(path):
		merged, err := MergeJSONL(local, remote)
		return merged, true, err
	case isStructuredJSONName(path):
		return nil, false, nil
	default:
		return MergeLines(local, remote), true, nil
	}
}

// MergeLines returns the union of two text blobs: local is preserved verbatim
// and any remote lines not already present in local are appended. Remote lines
// are de-duplicated against each other too. This is intentionally conservative
// (local-first, append-only) so it never reorders or drops local content — the
// best that can be done for unstructured text like Markdown.
func MergeLines(local, remote []byte) []byte {
	if len(local) == 0 {
		return remote
	}
	if len(remote) == 0 {
		return local
	}

	localLines := splitLines(local)
	present := make(map[string]struct{}, len(localLines))
	for _, l := range localLines {
		present[l] = struct{}{}
	}

	var add []string
	for _, r := range splitLines(remote) {
		if _, ok := present[r]; ok {
			continue
		}
		present[r] = struct{}{}
		add = append(add, r)
	}
	if len(add) == 0 {
		return local
	}

	out := strings.Join(append(localLines, add...), "\n")
	// Preserve a trailing newline if either side had one.
	if strings.HasSuffix(string(local), "\n") || strings.HasSuffix(string(remote), "\n") {
		out += "\n"
	}
	return []byte(out)
}

// splitLines splits text into lines, dropping the trailing-newline artifact so a
// final "\n" does not become a spurious empty line. Internal blank lines are
// preserved.
func splitLines(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func isJSONLName(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".jsonl")
}

func isStructuredJSONName(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".json") && !strings.HasSuffix(lower, ".jsonl")
}
