package parser

import (
	"bufio"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// UserDayStats holds the per-user line ownership at the end of a day on
// which at least one commit was made.
type UserDayStats struct {
	Date  string // "DD-MM-YYYY"
	Users map[string]int64
}

// UserLocOverTime reports, for each day that had at least one mainline
// commit, how many lines of the included file types were owned by each
// author as of that day's last commit. Ownership means "last commit to
// touch this line".
//
// Unlike LocOverTime (which accumulates diffs in a single pass), ownership
// cannot be updated incrementally: every snapshot needs a full `git blame`
// at that day's final commit, tracing each line back through history until
// its introducing commit is found. The days are independent, so they are
// blamed concurrently across runtime.NumCPU() workers; results are still
// returned in chronological order.
func UserLocOverTime(repo string, includeTypes map[string]bool) ([]UserDayStats, error) {
	return userLocOverTime(repo, includeTypes, nil)
}

// UserLocOverTimeProgress behaves like UserLocOverTime but calls onDay with
// each snapshot as soon as it is ready (in chronological order), so callers
// can render progress live. done and total count days completed and expected.
func UserLocOverTimeProgress(repo string, includeTypes map[string]bool, onDay func(done int, total int, stats UserDayStats)) ([]UserDayStats, error) {
	return userLocOverTime(repo, includeTypes, onDay)
}

type dayResult struct {
	idx  int
	snap UserDayStats
	err  error
}

func userLocOverTime(repo string, includeTypes map[string]bool, onDay func(int, int, UserDayStats)) ([]UserDayStats, error) {
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

	// Fan the days out to one worker per CPU core. The results channel is
	// buffered for everything in flight, so workers never block on us.
	results := make(chan dayResult, total)
	work := make(chan int, total)
	for i := range days {
		work <- i
	}
	close(work)

	workers := runtime.NumCPU()
	if workers > total {
		workers = total
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				users, err := blameAuthorsAt(repo, commitsByDay[days[idx]], includeTypes)
				if err != nil {
					results <- dayResult{idx: idx, err: err}
					return
				}
				results <- dayResult{idx: idx, snap: UserDayStats{Date: flipDate(days[idx]), Users: users}}
			}
		}()
	}

	// Close results once the workers finish, without blocking emission: the
	// reassembly below runs concurrently with them so snapshots stream out
	// while later days are still being blamed.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Reassemble in chronological order, emitting each snapshot to onDay as
	// soon as its turn comes (later days may already be done and waiting).
	out := make([]UserDayStats, 0, total)
	pending := map[int]dayResult{}
	nextIdx, firstErr := 0, error(nil)
	for res := range results {
		if res.err != nil && firstErr == nil {
			firstErr = res.err
		}
		pending[res.idx] = res
		for {
			r, ok := pending[nextIdx]
			if !ok {
				break
			}
			delete(pending, nextIdx)
			out = append(out, r.snap)
			if onDay != nil {
				onDay(nextIdx+1, total, r.snap)
			}
			nextIdx++
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
// only files whose extension is in includeTypes. git blame accepts a single
// file per invocation, so this runs one blame per file and skips any that
// cannot be blamed (e.g. binaries).
func blameAuthorsAt(repo, commit string, includeTypes map[string]bool) (map[string]int64, error) {
	files, err := trackedFilesAt(repo, commit, includeTypes)
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

// trackedFilesAt lists the files present at commit whose extension is in
// includeTypes.
func trackedFilesAt(repo, commit string, includeTypes map[string]bool) ([]string, error) {
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
		if includeTypes[extOf(path)] {
			files = append(files, path)
		}
	}
	return files, nil
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
