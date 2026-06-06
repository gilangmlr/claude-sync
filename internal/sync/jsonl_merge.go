package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type jsonlLine struct {
	raw     string
	sortKey int64 // unix milliseconds; math.MinInt64 = no timestamp known yet
}

// MergeJSONL returns the timestamp-ordered, exact-line-deduplicated union of two
// JSONL byte streams. Local lines win on identical content and on equal
// timestamps (stable order: local before remote). Lines without a parseable
// timestamp carry forward the previous line's timestamp from their own source;
// leading lines with no predecessor sort to the front. Returns an error if any
// non-empty line in either input is not valid JSON.
func MergeJSONL(local, remote []byte) ([]byte, error) {
	localLines, err := parseJSONLLines(local)
	if err != nil {
		return nil, err
	}
	remoteLines, err := parseJSONLLines(remote)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(localLines)+len(remoteLines))
	merged := make([]jsonlLine, 0, len(localLines)+len(remoteLines))
	add := func(lines []jsonlLine) {
		for _, ln := range lines {
			if _, ok := seen[ln.raw]; ok {
				continue
			}
			seen[ln.raw] = struct{}{}
			merged = append(merged, ln)
		}
	}
	add(localLines)
	add(remoteLines)

	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].sortKey < merged[j].sortKey
	})

	if len(merged) == 0 {
		return []byte{}, nil
	}
	var buf bytes.Buffer
	for _, ln := range merged {
		buf.WriteString(ln.raw)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// parseJSONLLines splits data into non-blank lines, validates each as JSON, and
// resolves a sort key per line (carry-forward for missing timestamps).
func parseJSONLLines(data []byte) ([]jsonlLine, error) {
	var out []jsonlLine
	prevKey := int64(math.MinInt64)
	havePrev := false
	for _, rawBytes := range bytes.Split(data, []byte("\n")) {
		raw := strings.TrimRight(string(rawBytes), "\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return nil, fmt.Errorf("invalid JSON line %q: %w", raw, err)
		}
		key, ok := parseTimestampMillis(obj["timestamp"])
		if ok {
			prevKey = key
			havePrev = true
		} else if havePrev {
			key = prevKey
		} else {
			key = math.MinInt64
		}
		out = append(out, jsonlLine{raw: raw, sortKey: key})
	}
	return out, nil
}

// parseTimestampMillis converts a JSON timestamp value to unix milliseconds.
// Accepts integer epoch (auto-detecting seconds vs milliseconds) and ISO-8601
// strings. Returns ok=false when absent or unparseable.
func parseTimestampMillis(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		n := int64(num)
		if n <= 0 {
			return 0, false
		}
		if n >= 1_000_000_000_000 { // >= 1e12 -> already milliseconds
			return n, true
		}
		return n * 1000, true // otherwise seconds
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return 0, false
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UnixMilli(), true
			}
		}
	}
	return 0, false
}
