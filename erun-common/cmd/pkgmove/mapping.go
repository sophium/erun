package main

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strings"
)

// rootDir is the module root package's directory in a mapping.
const rootDir = "."

// packageMap says which package directory every file and declaration of the
// module belongs to. Directories are relative to the module root; rootDir is
// the root package itself.
type packageMap struct {
	rules     []fileRule
	overrides map[declKey]string
	renames   map[string]string
}

type fileRule struct {
	dir     string
	pattern string
}

// declKey names a top-level declaration by the file it is declared in today
// and its name.
type declKey struct {
	file string
	name string
}

func parseMap(r io.Reader) (packageMap, error) {
	m := packageMap{overrides: map[declKey]string{}, renames: map[string]string{}}
	scanner := bufio.NewScanner(r)
	line := 0
	for scanner.Scan() {
		line++
		if err := m.parseLine(scanner.Text()); err != nil {
			return packageMap{}, fmt.Errorf("map line %d: %w", line, err)
		}
	}
	return m, scanner.Err()
}

func (m *packageMap) parseLine(text string) error {
	if i := strings.Index(text, "#"); i >= 0 {
		text = text[:i]
	}
	fields := strings.Fields(text)
	switch {
	case len(fields) == 0:
		return nil
	case len(fields) == 3 && fields[0] == "rename":
		m.renames[fields[1]] = fields[2]
		return nil
	case len(fields) != 2:
		return fmt.Errorf("want `<dir> <file-glob>`, `<dir> <file>:<Ident>` or `rename <old> <new>`, got %q", text)
	}
	dir := path.Clean(fields[0])
	if file, name, ok := strings.Cut(fields[1], ":"); ok {
		m.overrides[declKey{file: file, name: name}] = dir
		return nil
	}
	if _, err := path.Match(fields[1], ""); err != nil {
		return fmt.Errorf("bad pattern %q: %w", fields[1], err)
	}
	m.rules = append(m.rules, fileRule{dir: dir, pattern: fields[1]})
	return nil
}

// fileDir is the package directory a file belongs to: the first rule whose
// pattern matches it. A pattern without a slash matches a file's base name,
// so a rule written for a source file also moves its tests when they share a
// prefix; a test file no rule names follows its source file. A file nothing
// names stays where it is.
func (m packageMap) fileDir(rel string) string {
	if dir, ok := m.matchRule(rel); ok {
		return dir
	}
	if stem, ok := strings.CutSuffix(rel, "_test.go"); ok {
		for _, source := range []string{stem + ".go", trimOSSuffix(stem) + ".go"} {
			if dir, ok := m.matchRule(source); ok {
				return dir
			}
		}
	}
	return path.Dir(rel)
}

func (m packageMap) matchRule(rel string) (string, bool) {
	for _, rule := range m.rules {
		subject := rel
		if !strings.Contains(rule.pattern, "/") {
			if path.Dir(rel) != rootDir {
				continue
			}
			subject = path.Base(rel)
		}
		if ok, _ := path.Match(rule.pattern, subject); ok {
			return rule.dir, true
		}
	}
	return "", false
}

var osSuffixes = []string{"_darwin", "_linux", "_windows", "_unix", "_other"}

func trimOSSuffix(stem string) string {
	for _, suffix := range osSuffixes {
		if trimmed, ok := strings.CutSuffix(stem, suffix); ok {
			return trimmed
		}
	}
	return stem
}

// declDir is where a top-level declaration goes: its override if it has one,
// otherwise its file's directory. An override names its file the way a rule
// does: a bare name is a root package file, a path names any other.
func (m packageMap) declDir(rel, name string) string {
	key := declKey{file: rel, name: name}
	if path.Dir(rel) == rootDir {
		key.file = path.Base(rel)
	}
	if dir, ok := m.overrides[key]; ok {
		return dir
	}
	return m.fileDir(rel)
}
