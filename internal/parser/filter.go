package parser

import "strings"

// Filter decides which paths count toward a repo's stats: the language
// allowlist (if any), configured exclude dirs, and the repo's .gitignore
// rules.
type Filter struct {
	types       map[string]bool
	excludeDirs []string
	checker     *IgnoreChecker
}

func NewFilter(repo string, cfg *Config) *Filter {
	f := &Filter{excludeDirs: cfg.ExcludeDirs, checker: NewIgnoreChecker(repo)}
	if len(cfg.Languages) > 0 {
		f.types = make(map[string]bool, len(cfg.Languages))
		for _, l := range cfg.Languages {
			l = strings.ToLower(strings.TrimPrefix(l, "."))
			if l != "" {
				f.types[l] = true
			}
		}
	}
	return f
}

// MarkPaths resolves ignore status for the given paths in one batched git
// call (paths already resolved are skipped). Safe for concurrent use.
func (f *Filter) MarkPaths(paths []string) {
	f.checker.Mark(paths)
}

// Included reports whether path counts: its language is allowed, it is not
// under an excluded dir, and it is not ignored by the repo's .gitignore files.
func (f *Filter) Included(path string) bool {
	if len(f.types) > 0 && !f.types[extOf(path)] {
		return false
	}
	for _, d := range f.excludeDirs {
		d = strings.Trim(d, "/")
		if d != "" && strings.Contains("/"+path, "/"+d+"/") {
			return false
		}
	}
	return !f.checker.Ignored(path)
}
