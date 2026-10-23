// Command buildcost measures what one edit to erun-common costs every module
// that builds on it: how much the Go build cache grows and how long the
// rebuild takes.
//
//	buildcost -repo DIR -edit GateMergeWorkingTree -edit RunDeploySpec ...
//
// It warms an isolated build cache by running each module's gate commands
// (build, vet, compile every test) once, then for each edit appends an
// exported declaration to the file that declares the named function, so the
// package's export data changes the way a real edit's does, runs the same
// commands again, and records the cache growth and wall time. The file is
// restored after every edit. An edit is named by a declaration rather than a
// path so the same edit can be measured before and after a package move.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// modules are the modules whose gates an erun-common edit reaches.
var modules = []string{
	"erun-common",
	"erun-cli",
	"erun-mcp",
	"erun-ui",
	"erun-backend/erun-backend-api",
}

// gate is what a developer or a build runs in a module after an edit.
var gate = [][]string{
	{"build", "./..."},
	{"vet", "./..."},
	{"test", "-count=1", "-run", "^$", "./..."},
}

type editList []string

func (e *editList) String() string     { return strings.Join(*e, ",") }
func (e *editList) Set(v string) error { *e = append(*e, v); return nil }

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "buildcost:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("buildcost", flag.ContinueOnError)
	repo := fs.String("repo", "..", "repository root")
	cache := fs.String("cache", "", "build cache directory (default: a fresh temporary one)")
	var edits editList
	fs.Var(&edits, "edit", "function whose declaring file to edit; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(edits) == 0 {
		return errors.New("at least one -edit is required")
	}
	m, err := newMeasurer(*repo, *cache)
	if err != nil {
		return err
	}
	if _, _, err := m.gate(); err != nil {
		return fmt.Errorf("warming the cache: %w", err)
	}
	_, _ = fmt.Fprintf(out, "%-36s %-60s %10s %10s\n", "edit", "file", "cache MB", "wall s")
	for _, name := range edits {
		if err := m.measure(out, name); err != nil {
			return err
		}
	}
	return nil
}

type measurer struct {
	repo  string
	cache string
}

func newMeasurer(repo, cache string) (measurer, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return measurer{}, err
	}
	if cache == "" {
		if cache, err = os.MkdirTemp("", "buildcost-cache-"); err != nil {
			return measurer{}, err
		}
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return measurer{}, err
	}
	return measurer{repo: abs, cache: cache}, nil
}

func (m measurer) measure(out io.Writer, name string) error {
	file, err := m.declaringFile(name)
	if err != nil {
		return err
	}
	original, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	defer func() { _ = os.WriteFile(file, original, 0o644) }()
	marker := fmt.Sprintf("\n// BuildcostMarker%s changes this package's export data.\nfunc BuildcostMarker%s() int { return %d }\n", name, name, time.Now().UnixNano())
	if err := os.WriteFile(file, append(append([]byte{}, original...), marker...), 0o644); err != nil {
		return err
	}
	grew, took, err := m.gate()
	if err != nil {
		return fmt.Errorf("after editing %s: %w", name, err)
	}
	rel, _ := filepath.Rel(m.repo, file)
	_, _ = fmt.Fprintf(out, "%-36s %-60s %10.1f %10.1f\n", name, rel, float64(grew)/1e6, took.Seconds())
	if err := os.WriteFile(file, original, 0o644); err != nil {
		return err
	}
	_, _, err = m.gate()
	return err
}

// declaringFile is the erun-common file that declares function name.
func (m measurer) declaringFile(name string) (string, error) {
	decl := regexp.MustCompile(`(?m)^func (\([^)]*\) )?` + regexp.QuoteMeta(name) + `\b`)
	var found string
	root := filepath.Join(m.repo, "erun-common")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		src, err := os.ReadFile(p)
		if err == nil && decl.Match(src) {
			found = p
			return filepath.SkipAll
		}
		return err
	})
	if found == "" && err == nil {
		err = fmt.Errorf("no erun-common file declares func %s", name)
	}
	return found, err
}

// gate runs every module's gate commands and reports how much the cache grew
// and how long it took.
func (m measurer) gate() (int64, time.Duration, error) {
	before, err := dirSize(m.cache)
	if err != nil {
		return 0, 0, err
	}
	start := time.Now()
	for _, mod := range modules {
		for _, args := range gate {
			if err := m.goCmd(filepath.Join(m.repo, mod), args); err != nil {
				return 0, 0, err
			}
		}
	}
	took := time.Since(start)
	after, err := dirSize(m.cache)
	return after - before, took, err
}

func (m measurer) goCmd(dir string, args []string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOCACHE="+m.cache)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: go %s: %w\n%s", dir, strings.Join(args, " "), err, tail(out))
	}
	return nil
}

func tail(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	return strings.Join(lines, "\n")
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err == nil {
			total += info.Size()
		}
		return err
	})
	return total, err
}
