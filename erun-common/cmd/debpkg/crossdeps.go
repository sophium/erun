package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// crossNoDocPath excludes foreign docs: differing man pages block multiarch co-installation.
const crossNoDocPath = "/etc/dpkg/dpkg.cfg.d/cross-nodoc"

const crossNoDoc = "path-exclude=/usr/share/man/*\npath-exclude=/usr/share/doc/*\n"

func parseCrossDepsFlags(args []string) (string, []string, error) {
	var targetArch string
	fs := flag.NewFlagSet("cross-deps", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&targetArch, "arch", "", "target architecture (Docker TARGETARCH)")
	if err := fs.Parse(args); err != nil {
		return "", nil, err
	}
	arch, err := debianArch(targetArch)
	if err != nil {
		return "", nil, err
	}
	if fs.NArg() == 0 {
		return "", nil, errors.New("name the -dev packages to install for the target architecture")
	}
	return arch, fs.Args(), nil
}

func (d debpkg) crossDeps(arch string, packages []string) error {
	if arch == d.hostArch {
		_, err := fmt.Fprintf(d.stdout, "native %s build: no cross toolchain needed\n", arch)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.crossNoDocPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(d.crossNoDocPath, []byte(crossNoDoc), 0o644); err != nil {
		return err
	}
	install := []string{"install", "-y", "--no-install-recommends", "crossbuild-essential-" + arch}
	for _, pkg := range packages {
		install = append(install, pkg+":"+arch)
	}
	for _, step := range [][]string{
		{"dpkg", "--add-architecture", arch},
		{"apt-get", "update"},
		append([]string{"apt-get"}, install...),
	} {
		if err := d.run.run("", nil, step[0], step[1:]...); err != nil {
			return err
		}
	}
	return nil
}
