package parser

import (
	"bytes"
	"os/exec"
	"strings"
	"sync"
)

// IgnoreChecker resolves which paths a repo's .gitignore files exclude, using
// `git check-ignore` so git's own pattern semantics apply (nested .gitignore
// files, negations, etc.). Results are cached; paths are checked in batches
// as they are first seen.
type IgnoreChecker struct {
	mu    sync.Mutex
	repo  string
	known map[string]bool // path -> ignored
}

func NewIgnoreChecker(repo string) *IgnoreChecker {
	return &IgnoreChecker{repo: repo, known: map[string]bool{}}
}

// Mark checks the given paths against the repo's ignore rules and records the
// results. Paths not yet resolved are batched into a single git call.
func (c *IgnoreChecker) Mark(paths []string) {
	c.mu.Lock()
	var todo []string
	for _, p := range paths {
		if _, ok := c.known[p]; !ok {
			todo = append(todo, p)
		}
	}
	c.mu.Unlock()

	if len(todo) == 0 {
		return
	}

	var stdin bytes.Buffer
	for _, p := range todo {
		stdin.WriteString(p)
		stdin.WriteByte(0)
	}
	// --no-index matches purely on ignore patterns: files that were committed
	// but match .gitignore must still be excluded from history stats.
	cmd := exec.Command("git", "-C", c.repo, "check-ignore", "--no-index", "--stdin", "-z")
	cmd.Stdin = &stdin
	// Exit status 1 just means nothing was ignored; the output is what matters.
	out, _ := cmd.Output()

	c.mu.Lock()
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			c.known[p] = true
		}
	}
	for _, p := range todo {
		if _, ok := c.known[p]; !ok {
			c.known[p] = false
		}
	}
	c.mu.Unlock()
}

func (c *IgnoreChecker) Ignored(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.known[path]
}
