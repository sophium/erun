package shared

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type ignorePattern struct {
	raw      string
	anchored bool
	dirOnly  bool
	negate   bool
	// base scopes the pattern to its nested .gitignore's subtree.
	base string
}

type IgnoreSet struct {
	Patterns []ignorePattern
}

// LoadNestedGitignores skips nested .dockerignore files, which Docker ignores.
func LoadNestedGitignores(contextDir string, sources []string, combined *IgnoreSet) error {
	visited := make(map[string]struct{}, len(sources))
	for _, src := range sources {
		full := filepath.Join(contextDir, src)
		info, err := os.Lstat(full)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if !info.IsDir() {
			continue
		}
		if err := walkNestedGitignores(contextDir, full, visited, combined); err != nil {
			return err
		}
	}
	return nil
}

func walkNestedGitignores(contextDir, full string, visited map[string]struct{}, combined *IgnoreSet) error {
	return filepath.WalkDir(full, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(contextDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if _, seen := visited[rel]; seen {
				return filepath.SkipDir
			}
			visited[rel] = struct{}{}
			return nil
		}
		if d.Name() != ".gitignore" {
			return nil
		}
		base := filepath.ToSlash(filepath.Dir(rel))
		if base == "." || base == "" {
			return nil // root, already loaded
		}
		set, err := LoadIgnoreFile(path, base)
		if err != nil {
			return err
		}
		combined.Patterns = append(combined.Patterns, set.Patterns...)
		return nil
	})
}

func LoadIgnoreFile(path, base string) (*IgnoreSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &IgnoreSet{}, nil
		}
		return nil, err
	}
	return ParseIgnoreData(data, base), nil
}

func ParseIgnoreData(data []byte, base string) *IgnoreSet {
	set := &IgnoreSet{}
	for _, raw := range strings.Split(string(data), "\n") {
		pattern, ok := parseIgnoreLine(raw, base)
		if !ok {
			continue
		}
		set.Patterns = append(set.Patterns, pattern)
	}
	return set
}

func parseIgnoreLine(raw, base string) (ignorePattern, bool) {
	line := strings.TrimRight(raw, "\r")
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ignorePattern{}, false
	}
	negate := false
	if strings.HasPrefix(line, "!") {
		negate = true
		line = strings.TrimSpace(line[1:])
		if line == "" {
			return ignorePattern{}, false
		}
	}
	anchored := false
	if strings.HasPrefix(line, "/") {
		anchored = true
		line = strings.TrimPrefix(line, "/")
	}
	dirOnly := false
	if strings.HasSuffix(line, "/") {
		dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	// gitignore anchors a pattern with a non-trailing slash.
	if !anchored && strings.Contains(line, "/") {
		anchored = true
	}
	if line == "" {
		return ignorePattern{}, false
	}
	return ignorePattern{
		raw:      filepath.ToSlash(line),
		anchored: anchored,
		dirOnly:  dirOnly,
		negate:   negate,
		base:     base,
	}, true
}

func (s *IgnoreSet) Matches(rel string, isDir bool) bool {
	if s == nil || len(s.Patterns) == 0 {
		return false
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	matched := false
	for _, p := range s.Patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if patternMatchesPath(p, rel) {
			matched = !p.negate
		}
	}
	return matched
}

func patternMatchesPath(p ignorePattern, rel string) bool {
	if p.raw == "" || p.raw == "." {
		return false
	}
	if p.base != "" {
		if rel == p.base || !strings.HasPrefix(rel, p.base+"/") {
			return false
		}
		rel = strings.TrimPrefix(rel, p.base+"/")
	}
	if p.anchored {
		return anchoredPatternMatches(p.raw, rel)
	}
	// Non-anchored gitignore patterns match a basename anywhere in the tree.
	for _, part := range strings.Split(rel, "/") {
		if globMatch(p.raw, part) {
			return true
		}
	}
	return false
}

// anchoredPatternMatches also matches ancestors so ignored dirs are pruned early.
func anchoredPatternMatches(raw, rel string) bool {
	if globMatch(raw, rel) {
		return true
	}
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		if globMatch(raw, prefix) {
			return true
		}
	}
	return false
}

// globMatch adds `**`, which matches across path separators.
func globMatch(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		ok, err := filepath.Match(pattern, name)
		return err == nil && ok
	}
	re, err := regexp.Compile(globToRegex(pattern))
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

func globToRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		i = writeGlobToken(&b, pattern, i)
	}
	b.WriteString("$")
	return b.String()
}

func writeGlobToken(b *strings.Builder, pattern string, i int) int {
	switch {
	case i+1 < len(pattern) && pattern[i] == '*' && pattern[i+1] == '*':
		b.WriteString(".*")
		i += 2
		// Collapse `**/` so `a/**/b` matches `a/b` as well as `a/x/b`.
		if i < len(pattern) && pattern[i] == '/' {
			i++
		}
		return i
	case pattern[i] == '*':
		b.WriteString("[^/]*")
		return i + 1
	case pattern[i] == '?':
		b.WriteString("[^/]")
		return i + 1
	case strings.ContainsRune(`.+()|^$\{}`, rune(pattern[i])):
		b.WriteByte('\\')
		b.WriteByte(pattern[i])
		return i + 1
	default:
		b.WriteByte(pattern[i])
		return i + 1
	}
}
