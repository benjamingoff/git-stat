package parser

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func GetGitDirPaths(root string) []string {
	if root == "~" || strings.HasPrefix(root, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(root, "~"), "/"))
		}
	}

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

func HasGitRepo() bool {
	entries, err := os.ReadDir("./.git")
	if err != nil {
		return false
	}
	return len(entries) > 0
}

func GetFileType(filePath string) (string, error) {
	parts := strings.Split(filePath, ".")
	if len(parts) == 0 {
		return "", fmt.Errorf("file type not found")
	}
	return parts[len(parts)-1], nil
}
