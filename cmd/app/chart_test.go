package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/benjamingoff/git-stat/internal/parser"
)

func TestFormatInt(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{5, "5"},
		{999, "999"},
		{1000, "1,000"},
		{1234, "1,234"},
		{1234567, "1,234,567"},
		{-42, "-42"},
		{-12345, "-12,345"},
		{-1234567, "-1,234,567"},
	}
	for _, c := range cases {
		if got := formatInt(c.n); got != c.want {
			t.Errorf("formatInt(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short string changed: %q", got)
	}
	if got := truncate("", 5); got != "" {
		t.Errorf("empty string = %q", got)
	}
	// Exact width is left alone.
	s := "12345678" // exactly 8 cells
	if got := truncate(s, 8); got != s {
		t.Errorf("exact-width string changed: %q", got)
	}
	// Long strings are cut to the width and end with an ellipsis.
	got := truncate("hello world, this is a long name", 12)
	if lipgloss.Width(got) > 12 {
		t.Errorf("truncated string too wide: %q (%d cells)", got, lipgloss.Width(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix: %q", got)
	}
	// Tiny widths still produce something bounded.
	if got := truncate("hello", 1); lipgloss.Width(got) > 1 {
		t.Errorf("tiny-width result too wide: %q (%d cells)", got, lipgloss.Width(got))
	}
}

func TestDistributeHeight(t *testing.T) {
	// Non-positive heights and empty/zero values yield no segments.
	if segs := distributeHeight(map[string]int64{"a": 5}, []string{"a"}, 0); len(segs) != 0 {
		t.Errorf("height 0 should give no segments, got %v", segs)
	}
	if segs := distributeHeight(map[string]int64{}, []string{"a"}, 10); len(segs) != 0 {
		t.Errorf("empty values should give no segments, got %v", segs)
	}
	if segs := distributeHeight(map[string]int64{"a": 0}, []string{"a"}, 10); len(segs) != 0 {
		t.Errorf("zero values should give no segments, got %v", segs)
	}

	// A single segment takes the full height.
	segs := distributeHeight(map[string]int64{"a": 5}, []string{"a"}, 10)
	if len(segs) != 1 || segs[0].idx != 0 || segs[0].h != 10 {
		t.Errorf("single segment = %v, want [{0 10}]", segs)
	}

	// Heights sum exactly to the requested height (largest-remainder method).
	segs = distributeHeight(map[string]int64{"a": 1, "b": 2}, []string{"a", "b"}, 4)
	var sum int
	byIdx := map[int]int{}
	for _, s := range segs {
		sum += s.h
		byIdx[s.idx] = s.h
	}
	if sum != 4 {
		t.Errorf("segments sum to %d, want 4: %v", sum, segs)
	}
	// The larger share gets the extra row (largest remainder).
	if byIdx[0] != 1 || byIdx[1] != 3 {
		t.Errorf("segments = %v, want a=1 b=3", segs)
	}

	// Segments come back in series order (bottom-up stacking) with proportional heights.
	segs = distributeHeight(map[string]int64{"a": 5, "b": 3, "c": 2}, []string{"a", "b", "c"}, 10)
	if len(segs) != 3 || segs[0].idx != 0 || segs[1].idx != 1 || segs[2].idx != 2 {
		t.Errorf("segments out of order: %v", segs)
	}
	if segs[0].h != 5 || segs[1].h != 3 || segs[2].h != 2 {
		t.Errorf("proportional split = %v, want a=5 b=3 c=2", segs)
	}
}

func TestComputeChartLayout(t *testing.T) {
	// Degenerate inputs produce an empty layout rather than dividing by zero.
	if l := computeChartLayout(0, 80); l != (chartLayout{}) {
		t.Errorf("n=0: got %+v", l)
	}
	if l := computeChartLayout(5, 0); l != (chartLayout{}) {
		t.Errorf("plotW=0: got %+v", l)
	}

	// Specific cases.
	l := computeChartLayout(5, 80)
	if l.bucketSize != 1 || l.numCols != 5 || l.colWidth != 15 || l.gap != 1 {
		t.Errorf("n=5 plotW=80: got %+v, want bucketSize=1 numCols=5 colWidth=15 gap=1", l)
	}
	l = computeChartLayout(100, 40)
	if l.bucketSize != 3 || l.numCols != 34 || l.colWidth != 1 {
		t.Errorf("n=100 plotW=40: got %+v, want bucketSize=3 numCols=34 colWidth=1", l)
	}

	// Invariants across a sweep of sizes: everything fits within the width and all days are covered.
	for n := 1; n <= 200; n++ {
		for _, plotW := range []int{10, 20, 40, 80, 120} {
			l := computeChartLayout(n, plotW)
			if l.numCols < 1 || l.colWidth < 1 || l.bucketSize < 1 {
				t.Errorf("n=%d plotW=%d: degenerate layout %+v", n, plotW, l)
				continue
			}
			if used := l.numCols*l.colWidth + (l.numCols-1)*l.gap; used > plotW {
				t.Errorf("n=%d plotW=%d: layout uses %d cells, exceeds width: %+v", n, plotW, used, l)
			}
			if covered := l.numCols * l.bucketSize; covered < n {
				t.Errorf("n=%d plotW=%d: layout covers only %d days: %+v", n, plotW, covered, l)
			}
		}
	}
}

func TestParseDay(t *testing.T) {
	got := parseDay("2024-01-15")
	if got.Year() != 2024 || got.Month() != time.January || got.Day() != 15 {
		t.Errorf("parseDay = %v, want 2024-01-15", got)
	}
	if zero := parseDay("not a date"); !zero.IsZero() {
		t.Errorf("invalid date should yield the zero time, got %v", zero)
	}
}

func TestVisibleStart(t *testing.T) {
	// 10 days spanning Jan 1..10, 2024 (times precomputed at load time).
	times := make([]time.Time, 10)
	for i := range times {
		times[i] = parseDay(fmt.Sprintf("2024-01-%02d", i+1))
	}

	// No range → everything visible.
	m := chartModel{rangeDays: 0, fileDays: make([]parser.DayStats, len(times)), times: times}
	if got := m.visibleStart(); got != 0 {
		t.Errorf("rangeDays=0: visibleStart = %d, want 0", got)
	}

	// Empty days → 0 (no panic on an empty times slice).
	m = chartModel{rangeDays: 7}
	if got := m.visibleStart(); got != 0 {
		t.Errorf("empty days: visibleStart = %d, want 0", got)
	}

	// Data within the range → start at 0.
	times3 := times[7:] // Jan 8..10 (3 days)
	m = chartModel{rangeDays: 7, fileDays: make([]parser.DayStats, len(times3)), times: times3}
	if got := m.visibleStart(); got != 0 {
		t.Errorf("data within range: visibleStart = %d, want 0", got)
	}

	// Data spanning more than the range → first day on/after cutoff (Jan 10 - 7d = Jan 3 → index 2).
	m = chartModel{rangeDays: 7, fileDays: make([]parser.DayStats, len(times)), times: times}
	if got := m.visibleStart(); got != 2 {
		t.Errorf("spanning range: visibleStart = %d, want 2 (Jan 3 is on the cutoff and included)", got)
	}

	// Month boundary: last day Feb 10, cutoff Jan 28 + ... → first day >= Feb 3.
	times4 := make([]time.Time, 0, 14)
	for d := time.Date(2024, 1, 28, 0, 0, 0, 0, time.UTC); !d.After(time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		times4 = append(times4, d)
	}
	m = chartModel{rangeDays: 7, fileDays: make([]parser.DayStats, len(times4)), times: times4}
	if got := m.visibleStart(); got != 6 { // Feb 3 is index 6 (Jan 28 + 6 days)
		t.Errorf("month boundary: visibleStart = %d, want 6", got)
	}
}

func TestSeekIndex(t *testing.T) {
	// 10 active days, one per day from Jan 1 to Jan 10 (2024).
	times := make([]time.Time, 10)
	for i := range times {
		times[i] = parseDay(fmt.Sprintf("2024-01-%02d", i+1))
	}
	target := func(days int) time.Time { return times[0].AddDate(0, 0, days) } // Jan 1 + N days

	// Forward: first active day at/after target (Jan 8 is index 7; Jan 5+3=Jan 8 → index 7).
	if got := seekIndex(times, 0, target(7), true); got != 7 {
		t.Errorf("forward to Jan 8: got %d, want 7", got)
	}
	// Forward with no day at/after target → clamped to the last day.
	if got := seekIndex(times, 0, times[9].AddDate(0, 1, 0), true); got != 9 {
		t.Errorf("forward past end: got %d, want 9 (clamped to last day)", got)
	}
	// Forward from a mid-list cursor only looks forward.
	if got := seekIndex(times, 5, target(7), true); got != 7 { // Jan 6 + ... wait: times[5]=Jan 6, target = Jan 1+7d = Jan 8 → index 7
		t.Errorf("forward from cursor 5: got %d, want 7", got)
	}
	// Backward: last active day at/before target (Jan 4 is index 3; Jan 1+3=Jan 4 → index 3).
	if got := seekIndex(times, 9, target(3), false); got != 3 {
		t.Errorf("backward to Jan 4: got %d, want 3", got)
	}
	// Backward with no day at/before target → clamped to the first day.
	if got := seekIndex(times, 9, times[0].AddDate(0, -1, 0), false); got != 0 {
		t.Errorf("backward past start: got %d, want 0 (clamped to first day)", got)
	}
	// Backward from a mid-list cursor only looks backward.
	if got := seekIndex(times, 5, target(3), false); got != 3 { // times[5]=Jan 6, target Jan 4 → index 3
		t.Errorf("backward from cursor 5: got %d, want 3", got)
	}
	// Empty / out-of-range inputs are no-ops (cursor returned unchanged).
	if got := seekIndex(nil, 0, target(7), true); got != 0 {
		t.Errorf("empty times: got %d, want 0", got)
	}
	if got := seekIndex(times, -1, target(7), true); got != -1 {
		t.Errorf("negative cursor: got %d, want -1", got)
	}
	if got := seekIndex(times, 99, target(7), true); got != 99 {
		t.Errorf("out-of-range cursor: got %d, want 99", got)
	}
}
