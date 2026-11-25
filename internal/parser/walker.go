package parser

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GetGitDirPaths returns the paths of all git repositories found under root,
// sorted alphabetically by path. It does not descend into a repository's working
// tree once one is recorded (nested repos are not listed) and never descends into
// .git internals. Unreadable entries are skipped silently.
func GetGitDirPaths(root string) []string {
	var repos []string
	found := map[string]bool{} // cleaned paths of already-recorded repos

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		p := filepath.Clean(path)
		for r := range found {
			if p == r || strings.HasPrefix(p, r+string(os.PathSeparator)) {
				return fs.SkipDir // don't descend into a repo we already recorded
			}
		}
		if d.Name() == ".git" {
			parent := filepath.Clean(filepath.Dir(path))
			found[parent] = true
			repos = append(repos, parent)
			return fs.SkipDir // don't descend into .git internals
		}
		return nil
	})

	sort.Strings(repos)
	return repos
}
