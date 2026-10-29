package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

// debianArches maps Docker TARGETARCH to the Debian arch; only released arches are accepted.
var debianArches = map[string]string{
	"amd64": "amd64",
	"arm64": "arm64",
}

func debianArch(targetArch string) (string, error) {
	arch, ok := debianArches[targetArch]
	if !ok {
		return "", fmt.Errorf("unsupported TARGETARCH: %q (want amd64 or arm64)", targetArch)
	}
	return arch, nil
}

type packageSpec struct {
	Name             string
	Version          string
	Arch             string
	Maintainer       string
	Section          string
	Priority         string
	Homepage         string
	Description      string
	DescriptionLines []string
	Depends          string
	Recommends       string
	ShlibDeps        []string
	GoBuilds         []goBuild
	CGO              bool
	Tags             string
	Files            []packageFile
	Out              string
}

type goBuild struct {
	Dir        string
	Package    string
	Dest       string
	VersionVar string
}

type packageFile struct {
	Src  string
	Dest string
	Mode os.FileMode
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func parseBuildFlags(args []string) (packageSpec, error) {
	var spec packageSpec
	var versionFile, arch string
	var descriptionLines, shlibDeps, goBuilds, files listFlag
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&spec.Name, "name", "", "package name")
	fs.StringVar(&spec.Version, "version", "", "package version; empty reads --version-file")
	fs.StringVar(&versionFile, "version-file", "", "file holding the version when --version is empty")
	fs.StringVar(&arch, "arch", "", "target architecture (Docker TARGETARCH)")
	fs.StringVar(&spec.Maintainer, "maintainer", "", "Maintainer field")
	fs.StringVar(&spec.Section, "section", "", "Section field")
	fs.StringVar(&spec.Priority, "priority", "optional", "Priority field")
	fs.StringVar(&spec.Homepage, "homepage", "", "Homepage field")
	fs.StringVar(&spec.Description, "description", "", "Description summary")
	fs.Var(&descriptionLines, "description-line", "extended description line (repeatable)")
	fs.StringVar(&spec.Depends, "depends", "", "Depends field")
	fs.Var(&shlibDeps, "shlibdeps", "path inside the package of a binary whose libraries go into Depends (repeatable)")
	fs.StringVar(&spec.Recommends, "recommends", "", "Recommends field")
	fs.Var(&goBuilds, "go-build", "<module-dir>:<package>:<dest>[:<version-var>] (repeatable)")
	fs.BoolVar(&spec.CGO, "cgo", false, "compile with CGO, through the target's cross toolchain when it is foreign")
	fs.StringVar(&spec.Tags, "tags", "", "Go build tags")
	fs.Var(&files, "file", "<src>:<dest>[:<octal-mode>] (repeatable, mode 0644 by default)")
	fs.StringVar(&spec.Out, "out", "", "directory the .deb is written to")
	if err := fs.Parse(args); err != nil {
		return packageSpec{}, err
	}
	if fs.NArg() > 0 {
		return packageSpec{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	var err error
	if spec.Arch, err = debianArch(arch); err != nil {
		return packageSpec{}, err
	}
	if spec.Version, err = resolveVersion(spec.Version, versionFile); err != nil {
		return packageSpec{}, err
	}
	spec.DescriptionLines, spec.ShlibDeps = descriptionLines, shlibDeps
	if spec.GoBuilds, err = parseGoBuilds(goBuilds); err != nil {
		return packageSpec{}, err
	}
	if spec.Files, err = parsePackageFiles(files); err != nil {
		return packageSpec{}, err
	}
	return spec, validateSpec(spec)
}

// resolveVersion falls back to the version file with spaces removed.
func resolveVersion(version, versionFile string) (string, error) {
	if version = strings.TrimSpace(version); version != "" {
		return version, nil
	}
	if versionFile == "" {
		return "", errors.New("--version is empty and no --version-file names a fallback")
	}
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return "", err
	}
	if version = strings.Join(strings.Fields(string(data)), ""); version == "" {
		return "", fmt.Errorf("%s holds no version", versionFile)
	}
	return version, nil
}

func parseGoBuilds(values []string) ([]goBuild, error) {
	builds := make([]goBuild, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) < 3 || len(parts) > 4 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("--go-build %q: want <module-dir>:<package>:<dest>[:<version-var>]", value)
		}
		dest, err := packagePath(parts[2])
		if err != nil {
			return nil, fmt.Errorf("--go-build %q: %w", value, err)
		}
		build := goBuild{Dir: parts[0], Package: parts[1], Dest: dest}
		if len(parts) == 4 {
			build.VersionVar = parts[3]
		}
		builds = append(builds, build)
	}
	return builds, nil
}

func parsePackageFiles(values []string) ([]packageFile, error) {
	files := make([]packageFile, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) < 2 || len(parts) > 3 || parts[0] == "" {
			return nil, fmt.Errorf("--file %q: want <src>:<dest>[:<octal-mode>]", value)
		}
		dest, err := packagePath(parts[1])
		if err != nil {
			return nil, fmt.Errorf("--file %q: %w", value, err)
		}
		file := packageFile{Src: parts[0], Dest: dest, Mode: 0o644}
		if len(parts) == 3 {
			mode, err := strconv.ParseUint(parts[2], 8, 32)
			if err != nil || mode > 0o777 {
				return nil, fmt.Errorf("--file %q: mode must be octal permissions such as 0644", value)
			}
			file.Mode = os.FileMode(mode)
		}
		files = append(files, file)
	}
	return files, nil
}

func packagePath(dest string) (string, error) {
	clean := path.Clean(dest)
	if !strings.HasPrefix(dest, "/") || clean == "/" || strings.HasPrefix(clean, "/DEBIAN") {
		return "", fmt.Errorf("destination %q must be an absolute path inside the package, outside DEBIAN", dest)
	}
	return clean, nil
}

func validateSpec(spec packageSpec) error {
	required := [][2]string{
		{"--name", spec.Name},
		{"--maintainer", spec.Maintainer},
		{"--section", spec.Section},
		{"--description", spec.Description},
		{"--out", spec.Out},
	}
	for _, field := range required {
		if strings.TrimSpace(field[1]) == "" {
			return fmt.Errorf("%s is required", field[0])
		}
	}
	return nil
}
