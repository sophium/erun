package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// devopsDockerfileRunsByStage reads the erun-devops Dockerfile and returns each
// stage's RUN instructions, continuation lines joined. Reads a sibling module,
// so its tests run uncached (go test -count=1) in the image's cli-test stage.
func devopsDockerfileRunsByStage(t *testing.T) map[string][]string {
	t.Helper()
	path := filepath.Join(repoRootForDockerignoreTest(t), "erun-devops", "docker", "erun-devops", "Dockerfile")
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

// The Homebrew formula and Scoop manifest build erun and emcp from source on
// macOS and Windows, which no Linux build compiles: a darwin- or windows-only
// break in erun-common would ship and fail only on a user's machine. The
// cli-test stage cross-builds both modules for both systems, so every erun
// build proves those source builds compile.
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

// A bare `claude` typed in a pod shell resumes the directory's last
// conversation because /usr/local/bin/claude is erun, which dispatches on its
// argv[0]. The link must be made after the npm install that writes the real
// /usr/local/bin/claude, or that install would replace it.
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
