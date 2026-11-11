package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ConfigFileName is the optional per-repo configuration file, read from the
// repo root when present. A missing file means defaults: all languages and
// .gitignore-only exclusions.
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

// LoadConfig creates a Config for repo: if ConfigFileName exists at the repo
// root it is parsed, otherwise the defaults are returned (all languages,
// .gitignore-only exclusions). An invalid file also falls back to defaults.
func LoadConfig(repo string) *Config {
	cfg := &Config{}
	data, err := os.ReadFile(filepath.Join(repo, ConfigFileName))
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, cfg)
	return cfg
}
