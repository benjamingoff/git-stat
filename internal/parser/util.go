package parser

import (
	"path/filepath"
	"strings"
)

// extOf returns the file extension without the leading dot ("a/b/c.go" -> "go").
func extOf(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}

func flipDate(iso string) string {
	parts := strings.Split(iso, "-")
	if len(parts) != 3 {
		return iso
	}
	return parts[2] + "-" + parts[1] + "-" + parts[0]
}
