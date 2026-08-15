package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// desktopModulePath is erun-app's module path, which is exactly what a -X target
// for these vars must NOT be — see the guard below.
const desktopModulePath = "github.com/sophium/erun/erun-ui"

// stampTargets are the linker-settable build vars. Referencing each var here is
// deliberate: renaming one without updating this map stops compiling, so the
// guard cannot quietly start checking a name nothing declares.
var stampTargets = map[string]*string{
	"buildVersion":      &buildVersion,
	"buildCommit":       &buildCommit,
	"buildDate":         &buildDate,
	"buildSkillsSource": &buildSkillsSource,
}

// buildScriptsProducingTheDesktop is every script that builds erun-app,
// package-manager formulae included: a stamp only the module's own scripts get
// right still leaves every installed desktop unstamped.
var buildScriptsProducingTheDesktop = []string{
	"build.sh",
	"build.ps1",
	filepath.Join("..", "Formula", "erun.rb"),
	filepath.Join("..", "bucket", "erun.json"),
}

// TestBuildScriptsStampSymbolsThisPackageDeclares guards the one failure mode a
// linker stamp has: -X addresses a symbol by package path, and a path no package
// declares is dropped in silence rather than refused. These vars live in package
// main — whose symbols are named `main.x` whatever the module is called — so a
// module-path prefix builds a binary that looks correctly stamped and carries
// nothing, which is how the desktop shipped a skills source it never had.
func TestBuildScriptsStampSymbolsThisPackageDeclares(t *testing.T) {
	for _, script := range buildScriptsProducingTheDesktop {
		body := readBuildScript(t, script)
		for name := range stampTargets {
			if strings.Contains(body, desktopModulePath+"."+name+"=") {
				t.Errorf("%s stamps %s.%s; erun-app declares it in package main, so this stamps nothing", script, desktopModulePath, name)
			}
		}
		if !strings.Contains(body, "main.buildVersion=") {
			t.Errorf("%s builds erun-app without stamping its version", script)
		}
	}
}

// TestDesktopBuildScriptsStampTheSkillsSource keeps the resolution the desktop
// depends on wired at both ends. The stamp is the only thing that names this
// build's skills once the bundle has been copied out of its checkout, so a
// script that drops it silently returns the desktop to installing nothing. Only
// the module's own scripts: a package-manager build runs in a source tree it
// then deletes, so a path stamped there would name nothing on the target.
func TestDesktopBuildScriptsStampTheSkillsSource(t *testing.T) {
	for _, script := range []string{"build.sh", "build.ps1"} {
		if !strings.Contains(readBuildScript(t, script), "main.buildSkillsSource=") {
			t.Errorf("%s does not stamp main.buildSkillsSource; a desktop built by it installs no skills once it runs outside its checkout", script)
		}
	}
}

func readBuildScript(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}
