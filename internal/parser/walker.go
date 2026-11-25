package parser

import (
	"io/fs"
	"os"
	"path/filepath"
)

func GetGitDirPaths(root string) []string {
	var repos []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return fs.SkipDir
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if e.Name() == ".git" {
				repos = append(repos, path)
				return fs.SkipDir
			}
		}
		return nil
	})
	return repos
}
