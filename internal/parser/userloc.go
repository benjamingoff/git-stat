package parser

import (
	"bufio"
	"os/exec"
	"sort"
	"strings"
)

// UserDayStats holds the per-user line ownership at the end of a day on
// which at least one commit was made.
type UserDayStats struct {
	Date  string // "MM-DD-YYYY"
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
// its introducing commit is found. This is O(days x blame cost) and can be
// slow on large repos.
func UserLocOverTime(repo string, includeTypes map[string]bool) ([]UserDayStats, error) {
	commitsByDay, err := lastCommitPerDay(repo)
	if err != nil {
		return nil, err
	}

	days := make([]string, 0, len(commitsByDay))
	for day := range commitsByDay {
		days = append(days, day)
	}
	sort.Strings(days)

	out := make([]UserDayStats, 0, len(days))
	for _, day := range days {
		users, err := blameAuthorsAt(repo, commitsByDay[day], includeTypes)
		if err != nil {
			return nil, err
		}
		out = append(out, UserDayStats{Date: flipDate(day), Users: users})
	}
	return out, nil
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
