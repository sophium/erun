// Command debpkg assembles the Debian packages erun's linux package
// components ship, so their Dockerfile `package` stages run one plain command
// instead of shell logic. It runs inside that stage, on the build platform:
//
//	go -C /src/erun-common run ./cmd/debpkg cross-deps --arch "${TARGETARCH}" <dev-package>...
//	go -C /src/erun-common run ./cmd/debpkg build --name erun --arch "${TARGETARCH}" ... --out /deb
//
// `cross-deps` installs a foreign target's C toolchain and libraries through
// Debian multiarch, and does nothing for a native target. `build` compiles the
// package's Go binaries for the target, lays out its files, writes
// DEBIAN/control (taking Depends from dpkg-shlibdeps when asked) and packs
// <out>/<name>_<version>_<arch>.deb with dpkg-deb.
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

// debpkg is one invocation. hostArch is the architecture the tool runs on,
// which is the build platform the stage runs on; run executes the Debian and
// Go tools, so a test can stand in for them.
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
