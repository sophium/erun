package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// build packs spec from a fresh temporary root, so a rerun never packs a stale file.
func (d debpkg) build(spec packageSpec) error {
	root, err := os.MkdirTemp("", "debpkg-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	// The root is the archive's `./` entry, so it needs a directory's mode.
	if err := os.Chmod(root, 0o755); err != nil {
		return err
	}
	if err := d.layout(spec, root); err != nil {
		return err
	}
	if err := os.MkdirAll(spec.Out, 0o755); err != nil {
		return err
	}
	deb := filepath.Join(spec.Out, debFileName(spec))
	if err := d.run.run("", nil, "dpkg-deb", "--build", "--root-owner-group", root, deb); err != nil {
		return err
	}
	_, err = fmt.Fprintln(d.stdout, deb)
	return err
}

func (d debpkg) layout(spec packageSpec, root string) error {
	env, err := d.goBuildEnv(spec)
	if err != nil {
		return err
	}
	for _, build := range spec.GoBuilds {
		if err := d.goBuild(spec, build, env, root); err != nil {
			return err
		}
	}
	for _, file := range spec.Files {
		if err := copyPackageFile(file, root); err != nil {
			return err
		}
	}
	if spec.Depends, err = d.depends(spec, root); err != nil {
		return err
	}
	return writeControl(spec, root)
}

func debFileName(spec packageSpec) string {
	return spec.Name + "_" + spec.Version + "_" + spec.Arch + ".deb"
}

// goBuildEnv uses the target's cross toolchain and multiarch libraries for a
// foreign-architecture CGO build.
func (d debpkg) goBuildEnv(spec packageSpec) ([]string, error) {
	env := []string{"GOOS=linux", "GOARCH=" + spec.Arch, "CGO_ENABLED=0"}
	if !spec.CGO {
		return env, nil
	}
	env[2] = "CGO_ENABLED=1"
	if spec.Arch == d.hostArch {
		return env, nil
	}
	out, err := d.run.output("", nil, "dpkg-architecture", "-a", spec.Arch, "-qDEB_HOST_MULTIARCH")
	if err != nil {
		return nil, err
	}
	triple := strings.TrimSpace(out)
	if triple == "" {
		return nil, fmt.Errorf("dpkg-architecture names no multiarch triple for %s", spec.Arch)
	}
	return append(env,
		"CC="+triple+"-gcc",
		"CXX="+triple+"-g++",
		"PKG_CONFIG_LIBDIR=/usr/lib/"+triple+"/pkgconfig:/usr/share/pkgconfig",
	), nil
}

func goBuildArgs(spec packageSpec, build goBuild, dest string) []string {
	args := []string{"build", "-trimpath"}
	if spec.Tags != "" {
		args = append(args, "-tags", spec.Tags)
	}
	ldflags := "-s -w"
	if build.VersionVar != "" {
		ldflags += " -X " + build.VersionVar + "=" + spec.Version
	}
	return append(args, "-ldflags", ldflags, "-o", dest, build.Package)
}

func (d debpkg) goBuild(spec packageSpec, build goBuild, env []string, root string) error {
	dest := filepath.Join(root, filepath.FromSlash(build.Dest))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := d.run.run(build.Dir, env, "go", goBuildArgs(spec, build, dest)...); err != nil {
		return err
	}
	return os.Chmod(dest, 0o755)
}

func copyPackageFile(file packageFile, root string) error {
	dest := filepath.Join(root, filepath.FromSlash(file.Dest))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	src, err := os.Open(file.Src)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dest, file.Mode)
}

func (d debpkg) depends(spec packageSpec, root string) (string, error) {
	fields := []string{}
	if spec.Depends != "" {
		fields = append(fields, spec.Depends)
	}
	for _, binary := range spec.ShlibDeps {
		found, err := d.shlibDeps(spec.Arch, filepath.Join(root, filepath.FromSlash(binary)))
		if err != nil {
			return "", err
		}
		fields = append(fields, found)
	}
	return strings.Join(fields, ", "), nil
}

// shlibDeps needs a scratch debian/control for dpkg-shlibdeps to run.
func (d debpkg) shlibDeps(arch, binary string) (string, error) {
	tree, err := os.MkdirTemp("", "debpkg-shlibs-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tree) }()
	if err := os.MkdirAll(filepath.Join(tree, "debian"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tree, "debian", "control"), nil, 0o644); err != nil {
		return "", err
	}
	out, err := d.run.output(tree, []string{"DEB_HOST_ARCH=" + arch}, "dpkg-shlibdeps", "-O", binary)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if depends, ok := strings.CutPrefix(line, "shlibs:Depends="); ok && strings.TrimSpace(depends) != "" {
			return strings.TrimSpace(depends), nil
		}
	}
	return "", errors.New("dpkg-shlibdeps found no libraries for " + binary)
}

// controlFile orders fields as dpkg documents them.
func controlFile(spec packageSpec) string {
	lines := []string{
		"Package: " + spec.Name,
		"Version: " + spec.Version,
		"Section: " + spec.Section,
		"Priority: " + spec.Priority,
		"Architecture: " + spec.Arch,
		"Maintainer: " + spec.Maintainer,
	}
	if spec.Depends != "" {
		lines = append(lines, "Depends: "+spec.Depends)
	}
	if spec.Recommends != "" {
		lines = append(lines, "Recommends: "+spec.Recommends)
	}
	if spec.Homepage != "" {
		lines = append(lines, "Homepage: "+spec.Homepage)
	}
	lines = append(lines, "Description: "+spec.Description)
	for _, line := range spec.DescriptionLines {
		lines = append(lines, " "+line)
	}
	return strings.Join(lines, "\n") + "\n"
}

func writeControl(spec packageSpec, root string) error {
	dir := filepath.Join(root, "DEBIAN")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "control")
	if err := os.WriteFile(path, []byte(controlFile(spec)), 0o644); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}
