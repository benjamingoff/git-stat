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
	Date  string // "DD-MM-YYYY"
	Users map[string]int64
}

// UserLocOverTime reports, for each active day, how many lines of the included
// files each author owned as of that day's last commit (ownership = last commit
// to touch the line). Each snapshot needs a full `git blame`, so days are blamed
// concurrently and returned in chronological order.
func UserLocOverTime(repo string, filter *Filter) ([]UserDayStats, error) {
	return userLocOverTime(repo, filter, nil)
}

// UserLocOverTimeProgress behaves like UserLocOverTime but calls onDay with
// each snapshot as soon as it is ready (in completion order, not
// chronological), so callers can render progress live. done and total count
// days completed and expected; idx is the snapshot's position in the final
// chronological slice. The most recent day — what all-time summaries need —
// is blamed up front and delivered first; the remaining days stream in as
// their workers finish (older days tend to finish sooner, having less
// history to trace).
func UserLocOverTimeProgress(repo string, filter *Filter, onDay func(done int, total int, idx int, stats UserDayStats)) ([]UserDayStats, error) {
	return userLocOverTime(repo, filter, onDay)
}

type dayResult struct {
	idx  int
	snap UserDayStats
	err  error
}

func userLocOverTime(repo string, filter *Filter, onDay func(int, int, int, UserDayStats)) ([]UserDayStats, error) {
	commitsByDay, err := lastCommitPerDay(repo)
	if err != nil {
		return nil, err
	}

	days := make([]string, 0, len(commitsByDay))
	for day := range commitsByDay {
		days = append(days, day)
	}
	sort.Strings(days)
	total := len(days)

	// Blame the most recent day up front so callers streaming progress get
	// the all-time snapshot first, before the fan-out below starts. (Older
	// days are cheaper to blame, so queue order alone would not guarantee
	// this one arrives first.)
	out := make([]UserDayStats, total)
	firstErr := error(nil)
	doneCount := 0
	if total > 0 {
		doneCount = 1
		users, err := blameAuthorsAt(repo, commitsByDay[days[total-1]], filter)
		if err != nil {
			firstErr = err
		} else {
			out[total-1] = UserDayStats{Date: flipDate(days[total-1]), Users: users}
		}
		if onDay != nil {
			onDay(doneCount, total, total-1, out[total-1])
		}
	}

	// Fan the remaining days out to one worker per logical CPU. Workers are
	// mostly blocked waiting on `git blame` subprocesses, so more workers than
	// cores is possible (blocked goroutines are cheap) — but measured on real
	// repos it just adds disk/RAM contention without speeding things up. The
	// results channel is buffered for everything in flight, so workers never
	// block on us.
	results := make(chan dayResult, total)
	work := make(chan int, total)
	for i := total - 2; i >= 0; i-- {
		work <- i
	}
	close(work)

	workers := runtime.NumCPU()
	if workers > total-1 {
		workers = total - 1
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				users, err := blameAuthorsAt(repo, commitsByDay[days[idx]], filter)
				if err != nil {
					results <- dayResult{idx: idx, err: err}
					return
				}
				results <- dayResult{idx: idx, snap: UserDayStats{Date: flipDate(days[idx]), Users: users}}
			}
		}()
	}

	// Close results once the workers finish, without blocking emission: the
	// loop below runs concurrently with them so snapshots stream out while
	// other days are still being blamed.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Forward each snapshot to onDay in completion order; callers place it at
	// its idx. The returned slice is chronological (indexed by day).
	for res := range results {
		doneCount++
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
		} else {
			out[res.idx] = res.snap
		}
		if onDay != nil {
			onDay(doneCount, total, res.idx, res.snap)
		}
	}
	return out, firstErr
}

// lastCommitPerDay returns the sha of the last first-parent commit for each
// day ("YYYY-MM-DD") that has at least one.
func lastCommitPerDay(repo string) (map[string]string, error) {
	cmd := exec.Command("git", "-C", repo, "log", "--first-parent", "--reverse", "--date=short", "--format=C%H|%ad")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	byDay := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		marker, day, ok := strings.Cut(line, "|")
		if !ok || day == "" {
			continue
		}
		// --reverse walks oldest to newest, so the last entry for a day wins.
		byDay[day] = strings.TrimPrefix(marker, "C")
	}
	return byDay, nil
}

// blameAuthorsAt counts the lines owned by each author at commit, considering
// only files the filter includes. git blame accepts a single file per
// invocation, so this runs one blame per file and skips any that cannot be
// blamed (e.g. binaries).
func blameAuthorsAt(repo, commit string, filter *Filter) (map[string]int64, error) {
	files, err := trackedFilesAt(repo, commit, filter)
	if err != nil {
		return nil, err
	}

	users := map[string]int64{}
	for _, f := range files {
		counts, err := blameFile(repo, commit, f)
		if err != nil {
			continue
		}
		addUserCounts(users, counts)
	}
	return users, nil
}

func addUserCounts(dst, src map[string]int64) {
	for user, n := range src {
		dst[user] += n
	}
}

// trackedFilesAt lists the files present at commit that the filter includes.
// Ignore status is resolved in batches and cached across days by the filter.
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

// blameFile runs `git blame --porcelain` on path at commit and returns
// per-author line counts.
func blameFile(repo, commit, path string) (map[string]int64, error) {
	cmd := exec.Command("git", "-C", repo, "blame", "--porcelain", commit, "--", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Every line of the files appears exactly once as a tab-prefixed content
	// line. A record's header (including its author) is emitted only for the
	// first line of a run of consecutive lines from the same commit; later
	// lines in the run repeat just the record start, so the last seen author
	// stays valid across them.
	users := map[string]int64{}
	current := ""
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "author ") {
			current = strings.TrimPrefix(line, "author ")
		} else if current != "" && strings.HasPrefix(line, "\t") {
			users[current]++
		}
	}

	err = scanner.Err()
	if err == nil {
		err = cmd.Wait()
	}
	if err != nil {
		return nil, err
	}
	return users, nil
}
