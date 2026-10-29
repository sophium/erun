package main

import (
	"bytes"
	_ "embed"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

//go:embed suite_main_test.go.tmpl
var suiteMainTemplate string

const suiteMainFile = "suite_main_test.go"

const coreDir = "base/core"

var (
	testMainPattern = regexp.MustCompile(`(?m)^func TestMain\(`)
	packagePattern  = regexp.MustCompile(`(?m)^package (\w+)`)
	timingSeam      = regexp.MustCompile(`\bTimingRecordDirEnv\s*=\s*"([^"]+)"`)
)

// writeSuiteMains adds a generated TestMain to every tested package lacking
// one, when the timing seam is known.
func writeSuiteMains(rootDir, timingEnv string) (int, error) {
	if timingEnv == "" {
		timingEnv = timingSeamValue(filepath.Join(rootDir, filepath.FromSlash(coreDir)))
	}
	if timingEnv == "" {
		return 0, nil
	}
	tmpl, err := template.New("suite").Parse(suiteMainTemplate)
	if err != nil {
		return 0, err
	}
	written := 0
	err = filepath.WalkDir(rootDir, func(dir string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(rootDir, dir)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if skipsSuite(rel, d.Name()) {
			return filepath.SkipDir
		}
		ok, err := writeSuiteMain(tmpl, dir, timingEnv)
		if ok {
			written++
		}
		return err
	})
	return written, err
}

// skipsSuite skips cmd/ tools, fixtures and hidden directories.
func skipsSuite(rel, name string) bool {
	return rel == "cmd" || strings.HasPrefix(rel, "cmd/") || name == "testdata" || strings.HasPrefix(name, ".")
}

func writeSuiteMain(tmpl *template.Template, dir, timingEnv string) (bool, error) {
	pkg, hasTests, hasMain, err := scanPackageTests(dir)
	if err != nil || !hasTests || hasMain || pkg == "" {
		return false, err
	}
	data := struct{ Package, TimingEnv string }{Package: pkg, TimingEnv: timingEnv}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return false, err
	}
	src, err := format.Source(b.Bytes())
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(filepath.Join(dir, suiteMainFile), src, 0o644)
}

func scanPackageTests(dir string) (pkg string, hasTests, hasMain bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", false, false, err
		}
		if m := packagePattern.FindSubmatch(src); m != nil && !strings.HasSuffix(string(m[1]), "_test") {
			pkg = string(m[1])
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			hasTests = true
			hasMain = hasMain || testMainPattern.Match(src)
		}
	}
	return pkg, hasTests, hasMain, nil
}

func timingSeamValue(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		if src, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			if m := timingSeam.FindSubmatch(src); m != nil {
				return string(m[1])
			}
		}
	}
	return ""
}
