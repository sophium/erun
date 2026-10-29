package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var buildStampTarget = regexp.MustCompile(`github\.com/sophium/erun/((?:cmd|erun-mcp)[\w/]*)\.(buildVersion|buildCommit|buildDate)\b`)

// A -X target naming a package that no longer declares the variable is
// silently ignored by the linker, and the binary reports "dev".
func TestBuildStampsTargetTheVariablesTheyStamp(t *testing.T) {
	root := repoRoot(t)
	sites := []string{
		"erun-cli/run.sh",
		"erun-cli/run.ps1",
		"erun-devops/docker/erun-devops/Dockerfile",
		"erun-devops/linux/erun-cli/Dockerfile",
		"Formula/erun.rb",
		"bucket/erun.json",
	}
	for _, site := range sites {
		data, err := os.ReadFile(filepath.Join(root, site))
		if err != nil {
			t.Fatalf("read %s: %v", site, err)
		}
		matches := buildStampTarget.FindAllStringSubmatch(string(data), -1)
		if len(matches) == 0 {
			t.Errorf("%s stamps no github.com/sophium/erun/cmd... build variable", site)
		}
		for _, m := range matches {
			dir := m[1]
			if strings.HasPrefix(dir, "cmd") {
				dir = filepath.Join("erun-cli", dir)
			}
			if !packageDeclares(t, filepath.Join(root, dir), m[2]) {
				t.Errorf("%s stamps %s, but %s declares no %s", site, m[0], dir, m[2])
			}
		}
	}
}

func packageDeclares(t *testing.T, dir, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^\s*` + name + `\s*=`)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if decl.Match(data) {
			return true
		}
	}
	return false
}
