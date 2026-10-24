package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// desktopModulePath is what a -X target must NOT use as its prefix.
const desktopModulePath = "github.com/sophium/erun/erun-ui"

// Referencing each var makes a rename fail to compile rather than go unchecked.
var stampTargets = map[string]*string{
	"buildVersion":      &buildVersion,
	"buildCommit":       &buildCommit,
	"buildDate":         &buildDate,
	"buildSkillsSource": &buildSkillsSource,
	"buildAgentsSource": &buildAgentsSource,
}

// buildScriptsProducingTheDesktop includes package-manager formulae.
var buildScriptsProducingTheDesktop = []string{
	"build.sh",
	"build.ps1",
	filepath.Join("..", "Formula", "erun.rb"),
	filepath.Join("..", "bucket", "erun.json"),
}

// -X silently drops an unknown symbol path; package main symbols are `main.x`, not module-prefixed.
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

// Only the module's own scripts: package-manager builds delete their source tree afterwards.
func TestDesktopBuildScriptsStampTheSkillsSource(t *testing.T) {
	for _, script := range []string{"build.sh", "build.ps1"} {
		if !strings.Contains(readBuildScript(t, script), "main.buildSkillsSource=") {
			t.Errorf("%s does not stamp main.buildSkillsSource; a desktop built by it installs no skills once it runs outside its checkout", script)
		}
	}
}

func TestDesktopBuildScriptsStampTheAgentsSource(t *testing.T) {
	for _, script := range []string{"build.sh", "build.ps1"} {
		if !strings.Contains(readBuildScript(t, script), "main.buildAgentsSource=") {
			t.Errorf("%s does not stamp main.buildAgentsSource; a desktop built by it installs no reusable agents once it runs outside its checkout", script)
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
