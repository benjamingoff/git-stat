package parser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRename(t *testing.T) {
	// Real formats observed from `git log --numstat` (see git's rename notation):
	cases := []struct{ in, wantOld, wantNew string }{
		{"src/{a.go => c.go}", "src/a.go", "src/c.go"},                       // braces, empty suffix
		{"src/c.go => sub2/d.go", "src/c.go", "sub2/d.go"},                   // plain cut across dirs
		{"b.txt => newdir/b2.txt", "b.txt", "newdir/b2.txt"},                 // plain cut to a new dir
		{"{sub3 => newdir2}/b.txt", "sub3/b.txt", "newdir2/b.txt"},           // braces, empty prefix
		{"a.go => c.go", "a.go", "c.go"},                                     // root-level rename
		{"src/{ => sub}/file.go", "src/file.go", "src/sub/file.go"},          // braces, empty old side (doubled slash)
		{"src/{sub/file.go => file2.go}", "src/sub/file.go", "src/file2.go"}, // braces with path-like sides
	}
	for _, c := range cases {
		oldPath, newPath := parseRename(c.in)
		if oldPath != c.wantOld || newPath != c.wantNew {
			t.Errorf("parseRename(%q) = (%q, %q), want (%q, %q)", c.in, oldPath, newPath, c.wantOld, c.wantNew)
		}
	}
}

func runGit(t *testing.T, repo string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func commitAll(t *testing.T, repo, date, msg string) {
	t.Helper()
	runGit(t, repo, nil, "add", "-A")
	env := []string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}
	runGit(t, repo, env, "commit", "-q", "-m", msg)
}

func writeFile(t *testing.T, repo, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertTypes(t *testing.T, got map[string]int64, want map[string]int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("types = %v (%d entries), want %v (%d entries)", got, len(got), want, len(want))
		return
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("types[%q] = %d, want %d (full: %v)", k, got[k], v, got)
		}
	}
}

func TestLocOverTime(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGit(t, repo, nil, "init", "-q")
	runGit(t, repo, nil, "config", "user.email", "test@example.com")
	runGit(t, repo, nil, "config", "user.name", "Test User")

	// Day 1: a.go (3 lines) + b.txt (2 lines).
	writeFile(t, repo, "a.go", "l1\nl2\nl3\n")
	writeFile(t, repo, "b.txt", "x\ny\n")
	commitAll(t, repo, "2024-01-01T10:00:00", "init")

	// Day 2: add a line to a.go (→ 4 lines); rename b.txt → c.go (content unchanged).
	writeFile(t, repo, "a.go", "l1\nl2\nl3\nl4\n")
	runGit(t, repo, nil, "mv", "b.txt", "c.go")
	commitAll(t, repo, "2024-01-02T10:00:00", "add line + rename")

	days, err := LocOverTime(repo, NewFilter(repo, &Config{}))
	if err != nil {
		t.Fatalf("LocOverTime: %v", err)
	}
	if len(days) != 2 {
		t.Fatalf("got %d days, want 2: %+v", len(days), days)
	}

	// Chronological order with exact dates.
	if days[0].Date != "2024-01-01" || days[1].Date != "2024-01-02" {
		t.Errorf("dates = %q, %q; want 2024-01-01, 2024-01-02", days[0].Date, days[1].Date)
	}

	// Day 1: cumulative counts at end of day.
	assertTypes(t, days[0].Types, map[string]int64{"go": 3, "txt": 2})

	// Day 2: a.go grew to 4 lines; the rename carried b.txt's 2 lines onto c.go (now .go).
	assertTypes(t, days[1].Types, map[string]int64{"go": 6})

	// Filtered run: only Go files count.
	daysFiltered, err := LocOverTime(repo, NewFilter(repo, &Config{Languages: []string{"go"}}))
	if err != nil {
		t.Fatalf("LocOverTime (filtered): %v", err)
	}
	if len(daysFiltered) != 2 {
		t.Fatalf("filtered: got %d days, want 2: %+v", len(daysFiltered), daysFiltered)
	}
	assertTypes(t, daysFiltered[0].Types, map[string]int64{"go": 3})
	assertTypes(t, daysFiltered[1].Types, map[string]int64{"go": 6})
}
