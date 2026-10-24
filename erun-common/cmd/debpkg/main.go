// Command debpkg cross-builds and packs the Debian packages erun's linux
// package components ship, from their Dockerfile `package` stages.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

func main() {
	tool := debpkg{hostArch: runtime.GOARCH, run: execRunner{}, stdout: os.Stdout, crossNoDocPath: crossNoDocPath}
	if err := tool.main(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "debpkg:", err)
		os.Exit(1)
	}
}

// debpkg is one invocation; run is a seam for the Debian and Go tools.
type debpkg struct {
	hostArch       string
	run            runner
	stdout         io.Writer
	crossNoDocPath string
}

func (d debpkg) main(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: debpkg build|cross-deps [flags]")
	}
	switch args[0] {
	case "build":
		spec, err := parseBuildFlags(args[1:])
		if err != nil {
			return err
		}
		return d.build(spec)
	case "cross-deps":
		arch, packages, err := parseCrossDepsFlags(args[1:])
		if err != nil {
			return err
		}
		return d.crossDeps(arch, packages)
	default:
		return fmt.Errorf("unknown command %q: want build or cross-deps", args[0])
	}
}
