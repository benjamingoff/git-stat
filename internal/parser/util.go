package parser

import (
	"path/filepath"
	"strings"
)

// extOf returns the file extension without the leading dot ("a/b/c.go" -> "go").
func extOf(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}
