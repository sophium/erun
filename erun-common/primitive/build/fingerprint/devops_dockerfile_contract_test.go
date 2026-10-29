package fingerprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reads a sibling module, so its tests run uncached (go test -count=1).
func devopsDockerfileRunsByStage(t *testing.T) map[string][]string {
	t.Helper()
	return dockerfileRunsByStage(t, filepath.Join(repoRootForDockerignoreTest(t), "erun-devops", "docker", "erun-devops", "Dockerfile"))
}

// dockerfileRunsByStage joins each RUN across its continuation lines, keyed by the stage it runs in.
func dockerfileRunsByStage(t *testing.T, path string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runs := map[string][]string{}
	stage := ""
	instruction := ""
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		// Docker drops comment lines even inside a continued instruction.
		if strings.HasPrefix(trimmed, "#") || (instruction == "" && trimmed == "") {
			continue
		}
		instruction += " " + strings.TrimSuffix(trimmed, "\\")
		if strings.HasSuffix(trimmed, "\\") {
			continue
		}
		fields := strings.Fields(instruction)
		instruction = ""
		switch strings.ToUpper(fields[0]) {
		case "FROM":
			stage = fields[len(fields)-1]
		case "RUN":
			runs[stage] = append(runs[stage], strings.Join(fields, " "))
		}
	}
	return runs
}

// Homebrew and Scoop build from source on macOS and Windows, which no Linux build compiles.
func TestDevopsCLITestStageCrossBuildsForHomebrewAndScoop(t *testing.T) {
	runs := devopsDockerfileRunsByStage(t)
	for _, module := range []string{"erun-cli", "erun-mcp"} {
		var step string
		for _, run := range runs["cli-test"] {
			if strings.Contains(run, "cd "+module+" ") {
				step = run
			}
		}
		if step == "" {
			t.Fatalf("the cli-test stage has no RUN step for %s", module)
		}
		for _, build := range []string{
			"CGO_ENABLED=0 GOOS=darwin go build ./...",
			"CGO_ENABLED=0 GOOS=windows go build ./...",
		} {
			if !strings.Contains(step, build) {
				t.Errorf("the cli-test step for %s must run %q: %s", module, build, step)
			}
		}
	}
}

// The link must follow the npm install that writes the real /usr/local/bin/claude.
func TestDevopsRuntimeLinksClaudeToErunAfterInstallingClaudeCode(t *testing.T) {
	const link = "ln -sf /usr/local/bin/erun /usr/local/bin/claude"
	installed := false
	for _, run := range devopsDockerfileRunsByStage(t)["runtime"] {
		if strings.Contains(run, "@anthropic-ai/claude-code@") {
			installed = true
		}
		if strings.Contains(run, link) {
			if !installed {
				t.Fatalf("the runtime stage links claude to erun before Claude Code is installed: %s", run)
			}
			return
		}
	}
	t.Fatalf("the runtime stage must run %q after installing Claude Code", link)
}
