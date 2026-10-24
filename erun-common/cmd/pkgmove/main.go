// Command pkgmove moves erun-common declarations between packages according
// to a map, rewriting references in the module and its importers.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "pkgmove:", err)
		os.Exit(1)
	}
}

func hostOS() string { return runtime.GOOS }

type options struct {
	mapFile   string
	module    string
	dir       string
	importers []string
	limit     int
	// timingEnv names the timing seam for a module with no base/core.
	timingEnv string
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: pkgmove plan|apply -map FILE [-importers DIR,...]")
	}
	opts, err := parseOptions(args[1:])
	if err != nil {
		return err
	}
	m, err := readMap(opts.mapFile)
	if err != nil {
		return err
	}
	w := newWorld(opts.module, opts.dir)
	if err := w.load(opts.dir, []string{"./..."}); err != nil {
		return err
	}
	switch args[0] {
	case "plan":
		return plan(w, m, opts, out)
	case "apply":
		return apply(w, m, opts, out)
	case "dump":
		return dump(w, m, out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("pkgmove", flag.ContinueOnError)
	opts := options{}
	var importers string
	fs.StringVar(&opts.mapFile, "map", "", "package map")
	fs.StringVar(&opts.module, "module", "github.com/sophium/erun/erun-common", "module path")
	fs.StringVar(&opts.dir, "dir", ".", "module directory")
	fs.StringVar(&importers, "importers", "", "comma-separated directories of importing modules")
	fs.IntVar(&opts.limit, "limit", 20, "references printed per cycle edge")
	fs.StringVar(&opts.timingEnv, "timing-env", "", "timing seam a generated suite redirects, when -dir has no base/core declaring it")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if opts.mapFile == "" {
		return options{}, errors.New("-map is required")
	}
	dir, err := filepath.Abs(opts.dir)
	if err != nil {
		return options{}, err
	}
	opts.dir = dir
	for _, imp := range strings.Split(importers, ",") {
		if imp = strings.TrimSpace(imp); imp != "" {
			abs, err := filepath.Abs(imp)
			if err != nil {
				return options{}, err
			}
			opts.importers = append(opts.importers, abs)
		}
	}
	return opts, nil
}

func readMap(name string) (packageMap, error) {
	f, err := os.Open(name)
	if err != nil {
		return packageMap{}, err
	}
	defer func() { _ = f.Close() }()
	return parseMap(f)
}

func plan(w *world, m packageMap, opts options, out io.Writer) error {
	d := w.decide(m)
	if !w.graph(d).report(out, d, opts.limit) {
		return errors.New("the map does not produce a sound package graph")
	}
	return nil
}
