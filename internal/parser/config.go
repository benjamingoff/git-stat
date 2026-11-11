package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ConfigFileName is the optional per-repo config file at the repo root; a missing file means defaults.
const ConfigFileName = ".gitstatconfig"

// Config controls which files git-stat counts for a repo.
type Config struct {
	// Languages lists the file extensions to include (e.g. "go", "ts").
	// When empty, all languages are included.
	Languages []string `json:"languages"`
	// ExcludeDirs lists directories to exclude in addition to the repo's
	// .gitignore rules (e.g. "vendor"). When empty, only .gitignore is used.
	ExcludeDirs []string `json:"exclude_dirs"`
}

// LoadConfig parses ConfigFileName from the repo root, falling back to defaults
// (all languages, .gitignore-only exclusions) if it is missing or invalid.
func LoadConfig(repo string) *Config {
	cfg := &Config{}
	data, err := os.ReadFile(filepath.Join(repo, ConfigFileName))
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, cfg)
	return cfg
}
