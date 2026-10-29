// Command buildcost measures the Go build cache growth and rebuild time one
// erun-common edit costs every dependent module.
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

var modules = []string{
	"erun-common",
	"erun-cli",
	"erun-mcp",
	"erun-ui",
	"erun-backend/erun-backend-api",
}

var gate = [][]string{
	{"build", "./..."},
	{"vet", "./..."},
	{"test", "-run", "^$", "./..."},
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
	_, _ = fmt.Fprintf(out, "%-36s %-56s %9s %8s  %s\n", "edit", "file", "cache MB", "wall s", "cache MB per module")
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
	parts := make([]string, 0, len(modules))
	for i, mod := range modules {
		parts = append(parts, fmt.Sprintf("%s %.1f", filepath.Base(mod), float64(grew.byModule[i])/1e6))
	}
	_, _ = fmt.Fprintf(out, "%-36s %-56s %9.1f %8.1f  %s\n", name, rel, float64(grew.total)/1e6, took.Seconds(), strings.Join(parts, ", "))
	if err := os.WriteFile(file, original, 0o644); err != nil {
		return err
	}
	_, _, err = m.gate()
	return err
}

// declaringFile searches erun-common first so an edit keeps its meaning across
// a package move.
func (m measurer) declaringFile(name string) (string, error) {
	decl := regexp.MustCompile(`(?m)^func (\([^)]*\) )?` + regexp.QuoteMeta(name) + `\b`)
	for _, mod := range modules {
		found, err := declaringFileIn(filepath.Join(m.repo, filepath.FromSlash(mod)), decl)
		if err != nil || found != "" {
			return found, err
		}
	}
	return "", fmt.Errorf("no module file declares func %s", name)
}

func declaringFileIn(root string, decl *regexp.Regexp) (string, error) {
	var found string
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
	if os.IsNotExist(err) {
		return "", nil
	}
	return found, err
}

type growth struct {
	total    int64
	byModule []int64
}

func (m measurer) gate() (growth, time.Duration, error) {
	g := growth{byModule: make([]int64, len(modules))}
	start := time.Now()
	for i, mod := range modules {
		before, err := dirSize(m.cache)
		if err != nil {
			return g, 0, err
		}
		for _, args := range gate {
			if err := m.goCmd(filepath.Join(m.repo, mod), args); err != nil {
				return g, 0, err
			}
		}
		after, err := dirSize(m.cache)
		if err != nil {
			return g, 0, err
		}
		g.byModule[i] = after - before
		g.total += after - before
	}
	return g, time.Since(start), nil
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
