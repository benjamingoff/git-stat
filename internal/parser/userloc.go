package parser

import (
	"bufio"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// UserDayStats holds per-user line ownership at the end of a day that had commits.
type UserDayStats struct {
	Date  string // "YYYY-MM-DD"
	Users map[string]int64
}

// cachedBlame is a per-file blame result and the day index it was computed for.
type cachedBlame struct {
	users map[string]int64
	day   int // chronological day index of the snapshot that produced this entry
}

// UserLocOverTimeProgress reports, for each active day, how many lines of the included
// files each author owned as of that day's last commit (ownership = last commit
// to touch the line). It calls onDay with each snapshot as soon as it is ready
// (completion order); idx is the snapshot's position in the returned chronological
// slice. The most recent day is blamed up front so it arrives first.
func UserLocOverTimeProgress(repo string, filter *Filter, onDay func(done int, total int, idx int, stats UserDayStats)) ([]UserDayStats, error) {
	days, commitsByDay, touchDays, err := ownerHistory(repo)
	if err != nil {
		return nil, err
	}

	total := len(days)
	out := make([]UserDayStats, total)
	doneCount := 0
	firstErr := error(nil)

	// Phase 1: the most recent day first (full blame of included files at that commit).
	if total > 0 {
		users, err := fullBlameAt(repo, commitsByDay[days[total-1]], filter)
		if err != nil {
			firstErr = err
			out[total-1] = UserDayStats{Date: days[total-1], Users: map[string]int64{}}
		} else {
			out[total-1] = UserDayStats{Date: days[total-1], Users: users}
		}
		doneCount++
		if onDay != nil {
			onDay(doneCount, total, total-1, out[total-1])
		}
	}

	// Phase 2: remaining days ascending with a rolling per-file blame cache. A file is
	// re-blamed only when it was touched on some day since the snapshot that cached it.
	cache := map[string]cachedBlame{}
	for idx := 0; idx < total-1; idx++ {
		commit := commitsByDay[days[idx]]
		files, err := trackedFilesAt(repo, commit, filter)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			doneCount++
			snap := UserDayStats{Date: days[idx], Users: map[string]int64{}}
			out[idx] = snap
			if onDay != nil {
				onDay(doneCount, total, idx, snap)
			}
			continue
		}

		users := map[string]int64{}
		var toBlame []string
		for _, f := range files {
			cb, ok := cache[f]
			if ok && !touchedSince(touchDays[f], cb.day, idx) {
				addUserCounts(users, cb.users)
				continue
			}
			toBlame = append(toBlame, f)
		}

		perFile := blameFilesParallel(repo, commit, toBlame)
		for _, f := range toBlame {
			counts := perFile[f] // nil when the file failed to blame; skip it (same as before)
			if counts == nil {
				continue
			}
			cache[f] = cachedBlame{users: counts, day: idx}
			addUserCounts(users, counts)
		}

		snap := UserDayStats{Date: days[idx], Users: users}
		out[idx] = snap
		doneCount++
		if onDay != nil {
			onDay(doneCount, total, idx, snap)
		}
	}

	return out, firstErr
}

// ownerHistory makes a single `git log --numstat` pass over mainline history and returns:
//   - days: sorted "YYYY-MM-DD" dates that have at least one commit
//   - commitsByDay: sha of the last first-parent commit for each day
//   - touchDays: per path, the ascending chronological day indices on which it was touched
func ownerHistory(repo string) ([]string, map[string]string, map[string][]int, error) {
	args := []string{
		"-C", repo, "log",
		"--first-parent", "--diff-merges=first-parent",
		"--reverse", "--numstat",
		"--date=short", "--format=C%H|%ad",
	}
	cmd := exec.Command("git", args...)
	raw, err := cmd.Output()
	if err != nil {
		return nil, nil, nil, err
	}

	daysSet := map[string]bool{}
	commitsByDay := map[string]string{}
	dirtyByDay := map[string]map[string]bool{} // day -> set of paths touched that day

	currentDay := ""
	for _, line := range strings.Split(string(raw), "\n") {
		// "C<sha>|<date>" marker; numstat lines start with a digit or '-', so 'C' is unambiguous.
		if strings.HasPrefix(line, "C") {
			i := strings.Index(line, "|")
			if i >= 0 {
				day := line[i+1:]
				if day != "" {
					currentDay = day
					daysSet[day] = true
					// --reverse walks oldest to newest, so the last entry for a day wins.
					commitsByDay[day] = strings.TrimPrefix(line[:i], "C")
				}
			}
			continue
		}

		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 || currentDay == "" {
			continue
		}
		path := fields[2]
		if dirtyByDay[currentDay] == nil {
			dirtyByDay[currentDay] = map[string]bool{}
		}
		if strings.Contains(path, " => ") {
			oldPath, newPath := parseRename(path)
			dirtyByDay[currentDay][oldPath] = true
			dirtyByDay[currentDay][newPath] = true
		} else {
			dirtyByDay[currentDay][path] = true
		}
	}

	days := make([]string, 0, len(daysSet))
	for d := range daysSet {
		days = append(days, d)
	}
	sort.Strings(days) // "YYYY-MM-DD" sorts chronologically as plain strings

	touchDays := map[string][]int{}
	for i, day := range days {
		for p := range dirtyByDay[day] {
			touchDays[p] = append(touchDays[p], i) // appended in ascending order
		}
	}
	return days, commitsByDay, touchDays, nil
}

// touchedSince reports whether a path was touched on some day t with cachedDay < t <= idx.
func touchedSince(touchDays []int, cachedDay, idx int) bool {
	if len(touchDays) == 0 {
		return false
	}
	i := sort.SearchInts(touchDays, idx+1) - 1 // last touch <= idx
	return i >= 0 && touchDays[i] > cachedDay
}

// fullBlameAt blames every included file at the given commit (parallel across files) and sums per-user counts.
func fullBlameAt(repo, commit string, filter *Filter) (map[string]int64, error) {
	files, err := trackedFilesAt(repo, commit, filter)
	if err != nil {
		return nil, err
	}
	users := map[string]int64{}
	for _, counts := range blameFilesParallel(repo, commit, files) {
		addUserCounts(users, counts)
	}
	return users, nil
}

// trackedFilesAt returns the included (filter-passing) paths in the tree at commit.
func trackedFilesAt(repo, commit string, filter *Filter) ([]string, error) {
	cmd := exec.Command("git", "-C", repo, "ls-tree", "-r", "-z", "--name-only", commit)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []string
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		files = append(files, path)
	}
	filter.MarkPaths(files)

	var included []string
	for _, p := range files {
		if filter.Included(p) {
			included = append(included, p)
		}
	}
	return included, nil
}

// fileBlame is one file's per-author line counts.
type fileBlame struct {
	path   string
	counts map[string]int64
}

// blameFilesParallel blames each file at commit with a bounded worker pool and returns
// per-file user counts. Files that fail to blame are omitted from the result.
func blameFilesParallel(repo, commit string, files []string) map[string]map[string]int64 {
	results := make(chan fileBlame, len(files))

	workers := runtime.NumCPU()
	if workers > len(files) {
		workers = len(files)
	}
	var wg sync.WaitGroup
	work := make(chan string)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range work {
				counts, err := blameFile(repo, commit, f)
				if err != nil {
					continue // skip failed files (same as before)
				}
				results <- fileBlame{f, counts}
			}
		}()
	}
	go func() {
		for _, f := range files {
			work <- f
		}
		close(work)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	out := make(map[string]map[string]int64, len(files))
	for r := range results {
		out[r.path] = r.counts
	}
	return out
}

func addUserCounts(dst, src map[string]int64) {
	for user, n := range src {
		dst[user] += n
	}
}

// blameFile returns per-author line counts for path at commit. It streams the porcelain
// output with an unbounded reader (no Scanner size limit), so long/minified lines are safe.
func blameFile(repo, commit, path string) (map[string]int64, error) {
	cmd := exec.Command("git", "-C", repo, "blame", "--porcelain", commit, "--", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// A record's author header is emitted only for the first line of a run from
	// the same commit, so the last seen author stays valid across later lines.
	users := map[string]int64{}
	current := ""
	reader := bufio.NewReader(stdout)
	for {
		rawLine, err := reader.ReadBytes('\n')
		if len(rawLine) > 0 {
			line := strings.TrimSuffix(string(rawLine), "\n")
			if strings.HasPrefix(line, "author ") {
				current = strings.TrimPrefix(line, "author ")
			} else if current != "" && strings.HasPrefix(line, "\t") {
				users[current]++
			}
		}
		if err != nil {
			break // io.EOF or a read error; cmd.Wait reports the real failure
		}
	}

	err = cmd.Wait()
	if err != nil {
		return nil, err
	}
	return users, nil
}
