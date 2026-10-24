package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type call struct {
	dir  string
	env  []string
	name string
	args []string
}

// fakeRunner snapshots the package root at dpkg-deb, since build removes it.
type fakeRunner struct {
	calls   []call
	outputs map[string]string
	packed  map[string]string
}

func (f *fakeRunner) run(dir string, env []string, name string, args ...string) error {
	f.calls = append(f.calls, call{dir, env, name, args})
	switch name {
	case "go":
		out := args[slices.Index(args, "-o")+1]
		return os.WriteFile(out, []byte("binary"), 0o600)
	case "dpkg-deb":
		f.packed = snapshotTree(args[len(args)-2])
	}
	return nil
}

func (f *fakeRunner) find(name string) call {
	for _, c := range f.calls {
		if c.name == name {
			return c
		}
	}
	return call{}
}

func (f *fakeRunner) output(dir string, env []string, name string, args ...string) (string, error) {
	f.calls = append(f.calls, call{dir, env, name, args})
	return f.outputs[name], nil
}

func snapshotTree(root string) map[string]string {
	tree := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		key := "./" + filepath.ToSlash(rel)
		if rel == "." {
			key = "./"
		}
		tree[key] = info.Mode().String()
		if rel == filepath.Join("DEBIAN", "control") {
			data, _ := os.ReadFile(path)
			tree["control"] = string(data)
		}
		return nil
	})
	return tree
}

func cliPackageArgs(t *testing.T, versionArgs ...string) []string {
	t.Helper()
	src := t.TempDir()
	license := filepath.Join(src, "LICENSE")
	if err := os.WriteFile(license, []byte("license\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"build", "--name", "erun", "--arch", "arm64", "--maintainer", "ERun <erun@local>",
		"--section", "utils", "--homepage", "https://github.com/sophium/erun",
		"--description", "Multi-tenant multi-environment deployment and management tool",
		"--description-line", "ERun ships the erun CLI together with the emcp and eapi executables.",
		"--go-build", "/src/erun-cli:.:/usr/bin/erun:github.com/sophium/erun/cmd/shared.buildVersion",
		"--go-build", "/src/erun-backend/erun-backend-api:./cmd/eapi:/usr/bin/eapi",
		"--file", license + ":/usr/share/doc/erun/copyright",
		"--out", filepath.Join(t.TempDir(), "deb"),
	}
	return append(args, versionArgs...)
}

func runTool(t *testing.T, hostArch string, outputs map[string]string, args []string) (*fakeRunner, string) {
	t.Helper()
	runner := &fakeRunner{outputs: outputs}
	var stdout bytes.Buffer
	tool := debpkg{hostArch: hostArch, run: runner, stdout: &stdout, crossNoDocPath: filepath.Join(t.TempDir(), "cross-nodoc")}
	if err := tool.main(args); err != nil {
		t.Fatal(err)
	}
	return runner, stdout.String()
}

func TestControlFileMatchesThePackageStagesFields(t *testing.T) {
	runner, stdout := runTool(t, "arm64", nil, cliPackageArgs(t, "--version", "1.2.3"))
	want := "Package: erun\n" +
		"Version: 1.2.3\n" +
		"Section: utils\n" +
		"Priority: optional\n" +
		"Architecture: arm64\n" +
		"Maintainer: ERun <erun@local>\n" +
		"Homepage: https://github.com/sophium/erun\n" +
		"Description: Multi-tenant multi-environment deployment and management tool\n" +
		" ERun ships the erun CLI together with the emcp and eapi executables.\n"
	if got := runner.packed["control"]; got != want {
		t.Fatalf("control =\n%s\nwant\n%s", got, want)
	}
	if !strings.HasSuffix(strings.TrimSpace(stdout), "erun_1.2.3_arm64.deb") {
		t.Fatalf("expected the .deb path on stdout, got %q", stdout)
	}
}

func TestPackageLayoutModesAndPacking(t *testing.T) {
	runner, _ := runTool(t, "arm64", nil, cliPackageArgs(t, "--version", "1.2.3"))
	want := map[string]string{
		"./":                             "drwxr-xr-x",
		"./DEBIAN":                       "drwxr-xr-x",
		"./DEBIAN/control":               "-rw-r--r--",
		"./usr":                          "drwxr-xr-x",
		"./usr/bin":                      "drwxr-xr-x",
		"./usr/bin/erun":                 "-rwxr-xr-x",
		"./usr/bin/eapi":                 "-rwxr-xr-x",
		"./usr/share":                    "drwxr-xr-x",
		"./usr/share/doc":                "drwxr-xr-x",
		"./usr/share/doc/erun":           "drwxr-xr-x",
		"./usr/share/doc/erun/copyright": "-rw-r--r--",
	}
	delete(runner.packed, "control")
	if len(runner.packed) != len(want) {
		t.Fatalf("package tree = %v, want %v", runner.packed, want)
	}
	for path, mode := range want {
		if runner.packed[path] != mode {
			t.Errorf("%s: mode %s, want %s", path, runner.packed[path], mode)
		}
	}
	pack := runner.calls[len(runner.calls)-1]
	if pack.name != "dpkg-deb" || !slices.Equal(pack.args[:2], []string{"--build", "--root-owner-group"}) || filepath.Base(pack.args[3]) != "erun_1.2.3_arm64.deb" {
		t.Fatalf("pack call = %+v", pack)
	}
}

func TestGoBuildsTargetTheArchitectureAndStampTheVersion(t *testing.T) {
	runner, _ := runTool(t, "amd64", nil, cliPackageArgs(t, "--version", "1.2.3"))
	builds := slices.DeleteFunc(slices.Clone(runner.calls), func(c call) bool { return c.name != "go" })
	if len(builds) != 2 {
		t.Fatalf("expected two go builds, got %+v", builds)
	}
	wantEnv := []string{"GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0"}
	for _, build := range builds {
		if !slices.Equal(build.env, wantEnv) {
			t.Errorf("env = %v, want %v", build.env, wantEnv)
		}
	}
	erun := builds[0]
	if erun.dir != "/src/erun-cli" || !slices.Equal(erun.args[:2], []string{"build", "-trimpath"}) ||
		erun.args[slices.Index(erun.args, "-ldflags")+1] != "-s -w -X github.com/sophium/erun/cmd/shared.buildVersion=1.2.3" ||
		erun.args[len(erun.args)-1] != "." {
		t.Errorf("erun build = %+v", erun)
	}
	eapi := builds[1]
	if eapi.args[slices.Index(eapi.args, "-ldflags")+1] != "-s -w" || eapi.args[len(eapi.args)-1] != "./cmd/eapi" {
		t.Errorf("eapi build = %+v", eapi)
	}
}

func TestCGOCrossBuildAndShlibDepends(t *testing.T) {
	args := []string{
		"build", "--name", "erun-desktop", "--version", "2.0.0", "--arch", "amd64",
		"--maintainer", "ERun <erun@local>", "--section", "devel", "--recommends", "erun",
		"--homepage", "https://github.com/sophium/erun", "--description", "ERun desktop application",
		"--cgo", "--tags", "desktop,production,webkit2_41",
		"--go-build", "/src/erun-ui:.:/usr/bin/erun-app:main.buildVersion",
		"--shlibdeps", "/usr/bin/erun-app",
		"--out", t.TempDir(),
	}
	outputs := map[string]string{
		"dpkg-architecture": "x86_64-linux-gnu\n",
		"dpkg-shlibdeps":    "shlibs:Depends=libc6 (>= 2.34), libgtk-3-0t64 (>= 3.0)\n",
	}
	runner, _ := runTool(t, "arm64", outputs, args)
	build, shlibs := runner.find("go"), runner.find("dpkg-shlibdeps")
	wantEnv := []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1", "CC=x86_64-linux-gnu-gcc", "CXX=x86_64-linux-gnu-g++", "PKG_CONFIG_LIBDIR=/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/share/pkgconfig"}
	if !slices.Equal(build.env, wantEnv) || build.args[slices.Index(build.args, "-tags")+1] != "desktop,production,webkit2_41" {
		t.Fatalf("cross build = %+v", build)
	}
	if !slices.Equal(shlibs.env, []string{"DEB_HOST_ARCH=amd64"}) || shlibs.args[0] != "-O" || !strings.HasSuffix(shlibs.args[1], "/usr/bin/erun-app") {
		t.Fatalf("shlibdeps call = %+v", shlibs)
	}
	if _, err := os.Stat(filepath.Join(shlibs.dir, "debian", "control")); err == nil {
		t.Fatal("the scratch shlibdeps tree must be removed afterwards")
	}
	control := runner.packed["control"]
	for _, line := range []string{"Depends: libc6 (>= 2.34), libgtk-3-0t64 (>= 3.0)\nRecommends: erun\nHomepage: ", "Section: devel\n", "Architecture: amd64\n"} {
		if !strings.Contains(control, line) {
			t.Errorf("control missing %q:\n%s", line, control)
		}
	}
}

func TestCGONativeBuildUsesTheDefaultToolchain(t *testing.T) {
	args := []string{
		"build", "--name", "erun-desktop", "--version", "2.0.0", "--arch", "arm64",
		"--maintainer", "m", "--section", "devel", "--description", "d", "--cgo",
		"--go-build", "/src/erun-ui:.:/usr/bin/erun-app", "--out", t.TempDir(),
	}
	runner, _ := runTool(t, "arm64", nil, args)
	for _, c := range runner.calls {
		if c.name == "dpkg-architecture" {
			t.Fatal("a native build needs no cross triple")
		}
		if c.name == "go" && !slices.Equal(c.env, []string{"GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=1"}) {
			t.Fatalf("native env = %v", c.env)
		}
	}
}

func TestShlibDependsMustFindLibraries(t *testing.T) {
	args := []string{
		"build", "--name", "x", "--version", "1", "--arch", "arm64", "--maintainer", "m",
		"--section", "devel", "--description", "d", "--shlibdeps", "/usr/bin/x",
		"--file", writeTemp(t, "x") + ":/usr/bin/x:0755", "--out", t.TempDir(),
	}
	tool := debpkg{hostArch: "arm64", run: &fakeRunner{outputs: map[string]string{"dpkg-shlibdeps": "shlibs:Depends=\n"}}, stdout: &bytes.Buffer{}}
	if err := tool.main(args); err == nil || !strings.Contains(err.Error(), "found no libraries") {
		t.Fatalf("expected an empty Depends to fail, got %v", err)
	}
}

func TestArchitectureMapping(t *testing.T) {
	for target, want := range map[string]string{"amd64": "amd64", "arm64": "arm64"} {
		if got, err := debianArch(target); err != nil || got != want {
			t.Errorf("debianArch(%q) = %q, %v", target, got, err)
		}
	}
	for _, target := range []string{"", "arm", "386", "riscv64"} {
		if _, err := debianArch(target); err == nil || !strings.Contains(err.Error(), "unsupported TARGETARCH") {
			t.Errorf("debianArch(%q) must be refused, got %v", target, err)
		}
	}
}

func TestVersionFallsBackToTheVersionFile(t *testing.T) {
	file := writeTemp(t, " 3.4.5\n")
	runner, _ := runTool(t, "arm64", nil, cliPackageArgs(t, "--version", "", "--version-file", file))
	if !strings.Contains(runner.packed["control"], "Version: 3.4.5\n") {
		t.Fatalf("control = %s", runner.packed["control"])
	}
	if _, err := resolveVersion("", ""); err == nil {
		t.Fatal("no version and no file must fail")
	}
	if _, err := resolveVersion("", writeTemp(t, "\n")); err == nil {
		t.Fatal("an empty version file must fail")
	}
}

func TestFlagsAreValidated(t *testing.T) {
	for name, args := range map[string][]string{
		"missing name":     {"--version", "1", "--arch", "arm64", "--maintainer", "m", "--section", "s", "--description", "d", "--out", "o"},
		"bad go-build":     {"--name", "n", "--version", "1", "--arch", "arm64", "--maintainer", "m", "--section", "s", "--description", "d", "--out", "o", "--go-build", "dir:pkg"},
		"relative dest":    {"--name", "n", "--version", "1", "--arch", "arm64", "--maintainer", "m", "--section", "s", "--description", "d", "--out", "o", "--file", "a:usr/bin/a"},
		"control dest":     {"--name", "n", "--version", "1", "--arch", "arm64", "--maintainer", "m", "--section", "s", "--description", "d", "--out", "o", "--file", "a:/DEBIAN/postinst"},
		"bad mode":         {"--name", "n", "--version", "1", "--arch", "arm64", "--maintainer", "m", "--section", "s", "--description", "d", "--out", "o", "--file", "a:/usr/bin/a:rwx"},
		"stray argument":   {"--name", "n", "--version", "1", "--arch", "arm64", "--maintainer", "m", "--section", "s", "--description", "d", "--out", "o", "extra"},
		"unsupported arch": {"--name", "n", "--version", "1", "--arch", "s390x", "--maintainer", "m", "--section", "s", "--description", "d", "--out", "o"},
	} {
		if _, err := parseBuildFlags(args); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestCrossDepsInstallsOnlyForAForeignTarget(t *testing.T) {
	runner, stdout := runTool(t, "arm64", nil, []string{"cross-deps", "--arch", "arm64", "libgtk-3-dev"})
	if len(runner.calls) != 0 || !strings.Contains(stdout, "native arm64 build") {
		t.Fatalf("a native target must install nothing, got %+v (%s)", runner.calls, stdout)
	}

	runner = &fakeRunner{}
	noDoc := filepath.Join(t.TempDir(), "dpkg.cfg.d", "cross-nodoc")
	tool := debpkg{hostArch: "arm64", run: runner, stdout: &bytes.Buffer{}, crossNoDocPath: noDoc}
	if err := tool.main([]string{"cross-deps", "--arch", "amd64", "libgtk-3-dev", "libsoup-3.0-dev"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range runner.calls {
		got = append(got, c.name+" "+strings.Join(c.args, " "))
	}
	want := []string{
		"dpkg --add-architecture amd64",
		"apt-get update",
		"apt-get install -y --no-install-recommends crossbuild-essential-amd64 libgtk-3-dev:amd64 libsoup-3.0-dev:amd64",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if data, err := os.ReadFile(noDoc); err != nil || string(data) != "path-exclude=/usr/share/man/*\npath-exclude=/usr/share/doc/*\n" {
		t.Fatalf("cross-nodoc = %q, %v", data, err)
	}
	if err := tool.main([]string{"cross-deps", "--arch", "amd64"}); err == nil {
		t.Fatal("cross-deps without packages must fail")
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
