package parser

import (
	"path/filepath"
	"strings"
)

// Shared helpers used by both stat walkers.

// extOf returns the file extension of path without the leading dot
// ("a/b/c.go" -> "go"; no extension -> "").
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
