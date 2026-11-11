package parser

import (
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// DayStats holds cumulative line counts per file type at the end of a day that had commits.
type DayStats struct {
	Date  string // "DD-MM-YYYY"
	Types map[string]int64
}

// LocOverTime accumulates per-file line counts from a single `git log --numstat`
// pass over mainline history, returning one snapshot per active day in
// chronological order. --first-parent limits merge commits to the net diff they
// brought into the mainline, so each snapshot equals that commit's tree.
func LocOverTime(repo string, filter *Filter) ([]DayStats, error) {
	args := []string{
		"-C", repo, "log",
		"--first-parent", "--diff-merges=first-parent",
		"--reverse", "--numstat",
		"--date=short", "--format=C%H|%ad",
	}
	cmd := exec.Command("git", args...)
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(raw), "\n")

	pathSet := map[string]bool{}
	for _, line := range lines {
		if strings.HasPrefix(line, "C") {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		if strings.Contains(fields[2], " => ") {
			oldPath, newPath := parseRename(fields[2])
			pathSet[oldPath] = true
			pathSet[newPath] = true
		} else {
			pathSet[fields[2]] = true
		}
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	filter.MarkPaths(paths)

	fileLines := map[string]int64{}       // path -> current line count
	days := map[string]map[string]int64{} // "YYYY-MM-DD" -> end-of-day snapshot

	// Called once per commit, so the last commit of a day wins.
	flushDay := func(day string) {
		if day == "" {
			return
		}
		snap := map[string]int64{}
		for path, n := range fileLines {
			if filter.Included(path) {
				snap[extOf(path)] += n
			}
		}
		for ext, n := range snap {
			if n == 0 {
				delete(snap, ext)
			}
		}
		days[day] = snap
	}

	bump := func(path string, delta int64) {
		if v := fileLines[path] + delta; v == 0 {
			delete(fileLines, path)
		} else {
			fileLines[path] = v
		}
	}

	currentDay := ""
	for _, line := range lines {
		// "C<sha>|<date>" marker; numstat lines start with a digit or '-', so 'C' is unambiguous.
		if strings.HasPrefix(line, "C") {
			if i := strings.Index(line, "|"); i >= 0 {
				flushDay(currentDay)
				currentDay = line[i+1:]
			}
			continue
		}

		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		addedS, removedS, path := fields[0], fields[1], fields[2]
		if addedS == "-" || removedS == "-" {
			continue // binary file
		}
		added, err := strconv.ParseInt(addedS, 10, 64)
		if err != nil {
			continue
		}
		removed, err := strconv.ParseInt(removedS, 10, 64)
		if err != nil {
			continue
		}

		if strings.Contains(path, " => ") {
			// Rename: carry the old path's line count to the new one, then apply the delta.
			oldPath, newPath := parseRename(path)
			oldLines := fileLines[oldPath]
			delete(fileLines, oldPath)
			delete(fileLines, newPath) // a rename replaces whatever was at the new path
			bump(newPath, oldLines+added-removed)
			continue
		}

		bump(path, added-removed)
	}
	flushDay(currentDay)

	// "YYYY-MM-DD" keys sort chronologically as plain strings.
	isos := make([]string, 0, len(days))
	for iso := range days {
		isos = append(isos, iso)
	}
	sort.Strings(isos)

	out := make([]DayStats, 0, len(isos))
	for _, iso := range isos {
		out = append(out, DayStats{Date: flipDate(iso), Types: days[iso]})
	}
	return out, nil
}

// parseRename expands git's "prefix{old => new}suffix" rename notation into full
// old and new paths, normalizing the doubled slash that appears when one side is empty.
func parseRename(path string) (oldPath, newPath string) {
	bopen := strings.Index(path, "{")
	bclose := strings.LastIndex(path, "}")
	if bopen >= 0 && bclose > bopen {
		prefix := path[:bopen]
		suffix := path[bclose+1:]
		inner := path[bopen+1 : bclose]
		o, n, _ := strings.Cut(inner, " => ")
		return normPath(prefix + o + suffix), normPath(prefix + n + suffix)
	}
	o, n, _ := strings.Cut(path, " => ")
	return o, n
}

func normPath(p string) string {
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return p
}
