package parser

import (
	"bufio"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DayStats holds the cumulative line count per file type at the end of a day
// on which at least one commit was made.
type DayStats struct {
	Date  string // "DD-MM-YYYY"
	Types map[string]int64
}

// LocOverTime walks a repo's mainline history (oldest to newest) in a single
// `git log --numstat` pass, accumulating per-file line counts from each
// commit's diff instead of re-counting the tree at every commit.
//
// The walk uses --first-parent so merge commits contribute the net diff they
// brought into the mainline; that keeps the accumulated state at each commit
// exactly equal to that commit's tree (summing diffs across side branches
// instead would double-count overlapping changes).
//
// It returns one snapshot per day that had at least one mainline commit, in
// chronological order. Only extensions present in includeTypes are counted.
func LocOverTime(repo string, includeTypes map[string]bool) ([]DayStats, error) {
	args := []string{
		"-C", repo, "log",
		"--first-parent", "--diff-merges=first-parent",
		"--reverse", "--numstat",
		"--date=short", "--format=C%H|%ad",
	}
	cmd := exec.Command("git", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stdout)
	fileLines := map[string]int64{}       // path -> current line count
	days := map[string]map[string]int64{} // "YYYY-MM-DD" -> end-of-day snapshot

	// flushDay records the running totals as the end-of-day state. It is
	// called once per commit, so the last commit of a day overwrites earlier
	// ones and wins.
	flushDay := func(day string) {
		if day == "" {
			return
		}
		snap := map[string]int64{}
		for path, n := range fileLines {
			if ext := extOf(path); includeTypes[ext] {
				snap[ext] += n
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
	for scanner.Scan() {
		line := scanner.Text()

		// Commit marker line: "C<sha>|<YYYY-MM-DD>". Numstat lines always
		// start with a digit or '-', so a leading 'C' is unambiguous.
		if strings.HasPrefix(line, "C") {
			if i := strings.Index(line, "|"); i >= 0 {
				flushDay(currentDay)
				currentDay = line[i+1:]
			}
			continue
		}

		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue // blank line or anything unexpected
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
			// Rename: carry the file's whole line count from the old path to
			// the new one, then apply the delta. This stays accurate for
			// renames that change the extension too.
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

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}

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

// parseRename expands git's rename notation "prefix{old => new}suffix" (or
// "old => new" when there is no shared part) into the full old and new
// paths. When one side of the rename is empty (a file moved into or out of a
// subdirectory), the shared prefix and suffix glue together with a doubled
// slash, which is normalized away.
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

func extOf(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}

// flipDate converts "YYYY-MM-DD" to "DD-MM-YYYY".
func flipDate(iso string) string {
	parts := strings.Split(iso, "-")
	if len(parts) != 3 {
		return iso
	}
	return parts[2] + "-" + parts[1] + "-" + parts[0]
}
