package ignore

import (
	"path/filepath"
)

// LoadContextIgnoreSet appends patterns in load order so later negations override.
func LoadContextIgnoreSet(contextDir string, sources []string) (*IgnoreSet, error) {
	combined := &IgnoreSet{}
	for _, name := range []string{".dockerignore", ".gitignore"} {
		set, err := LoadIgnoreFile(filepath.Join(contextDir, name), "")
		if err != nil {
			return nil, err
		}
		combined.Patterns = append(combined.Patterns, set.Patterns...)
	}
	if err := LoadNestedGitignores(contextDir, sources, combined); err != nil {
		return nil, err
	}
	return combined, nil
}
