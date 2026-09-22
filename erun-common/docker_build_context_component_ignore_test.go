package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeComponentTreeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func writeDockerfile(t *testing.T, root, rel string) {
	t.Helper()
	writeComponentTreeFile(t, root, rel, "FROM scratch\n")
}

func TestFindComponentDockerBuildContextResolvesSingleCandidate(t *testing.T) {
	root := t.TempDir()
	writeDockerfile(t, root, "erun-devops/docker/erun-dind/Dockerfile")

	context, ok, err := FindComponentDockerBuildContext(root, "erun-dind")
	if err != nil {
		t.Fatalf("FindComponentDockerBuildContext: %v", err)
	}
	if !ok {
		t.Fatal("expected the single real Dockerfile to resolve, got no match")
	}
	want := filepath.Join(root, "erun-devops", "docker", "erun-dind")
	if context.Dir != want {
		t.Fatalf("build context dir = %q, want %q", context.Dir, want)
	}
	if context.DockerfilePath != filepath.Join(want, "Dockerfile") {
		t.Fatalf("dockerfile path = %q, want %q", context.DockerfilePath, filepath.Join(want, "Dockerfile"))
	}
}

func TestFindComponentDockerBuildContextRefusesTwoRealCandidates(t *testing.T) {
	root := t.TempDir()
	writeDockerfile(t, root, "erun-devops/docker/erun-dind/Dockerfile")
	writeDockerfile(t, root, "elsewhere/docker/erun-dind/Dockerfile")

	_, ok, err := FindComponentDockerBuildContext(root, "erun-dind")
	if err == nil {
		t.Fatal("expected a genuinely ambiguous tree to be refused, got no error")
	}
	if !strings.Contains(err.Error(), "multiple Docker build contexts found") {
		t.Fatalf("error = %v, want a multiple-build-contexts refusal", err)
	}
	if ok {
		t.Fatal("expected ok=false on refusal")
	}
}

// A Dockerfile copy that the build context would never contain must not be
// counted. An agent worktree holds a full second copy of the tree and a
// third-party package can ship a docker/<name>/Dockerfile of its own; before
// the shared ignore matcher these made every component ambiguous.
func TestFindComponentDockerBuildContextIgnoresCopiesInIgnoredTrees(t *testing.T) {
	root := t.TempDir()
	writeDockerfile(t, root, "erun-devops/docker/erun-dind/Dockerfile")
	// Root .gitignore prunes the agent worktree, which duplicates every component.
	writeComponentTreeFile(t, root, ".gitignore", ".claude\n")
	writeDockerfile(t, root, ".claude/worktrees/agent-1/erun-devops/docker/erun-dind/Dockerfile")
	// A nested .gitignore prunes a third-party package's own docker context.
	writeComponentTreeFile(t, root, "erun-docs/.gitignore", "node_modules\n")
	writeDockerfile(t, root, "erun-docs/node_modules/comlink/docker/erun-dind/Dockerfile")

	context, ok, err := FindComponentDockerBuildContext(root, "erun-dind")
	if err != nil {
		t.Fatalf("FindComponentDockerBuildContext: %v", err)
	}
	if !ok {
		t.Fatal("expected the one tracked Dockerfile to resolve, got no match")
	}
	want := filepath.Join(root, "erun-devops", "docker", "erun-dind")
	if context.Dir != want {
		t.Fatalf("build context dir = %q, want %q", context.Dir, want)
	}
}
