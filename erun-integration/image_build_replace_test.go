package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImageDockerfilesCopyLocalReplaceTarget enforces the invariant that every
// erun-devops image Dockerfile building a module that locally replaces
// erun-common COPYs erun-common before its first `go mod download` — a cold
// (cache-miss) build otherwise fails resolving the missing local replace target.
func TestImageDockerfilesCopyLocalReplaceTarget(t *testing.T) {
	t.Parallel()
	// The integration suite also runs inside the erun-devops image build, whose
	// context copies only the Go modules, not the full source tree; this full-tree
	// guard no-ops there and runs on a full checkout.
	root, ok := findFullCheckoutRoot()
	if !ok {
		t.Skip("full source tree not present (partial in-build build context); this Dockerfile-content guard runs on a full checkout")
	}

	cases := []struct {
		name       string
		goMod      string
		dockerfile string
	}{
		{"erun-backend-api", "erun-backend/erun-backend-api/go.mod", "erun-devops/docker/erun-backend-api/Dockerfile"},
		{"erun-devops (erun + emcp)", "erun-cli/go.mod", "erun-devops/docker/erun-devops/Dockerfile"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goMod := mustReadRepoFile(t, root, tc.goMod)
			if !strings.Contains(goMod, "replace github.com/sophium/erun/erun-common => ") {
				t.Skipf("%s no longer replaces erun-common locally; invariant moot", tc.goMod)
			}

			df := mustReadRepoFile(t, root, tc.dockerfile)
			download := indexOfCommandLine(df, "go mod download")
			if download < 0 {
				t.Fatalf("%s builds %s but has no `go mod download` step", tc.dockerfile, tc.goMod)
			}
			copyCommon := indexOfCopyErunCommon(df)
			if copyCommon < 0 {
				t.Fatalf("%s builds erun-common-replacing module %s but never COPYs erun-common into the build context — a cold build fails at `go mod download` reading the ../../erun-common replace target (regression #691)", tc.dockerfile, tc.goMod)
			}
			if copyCommon > download {
				t.Fatalf("%s COPYs erun-common only after `go mod download`; the replace target must be present before the download (regression #691)", tc.dockerfile)
			}
		})
	}
}

// Matches only non-comment lines so a Dockerfile comment mentioning substr is
// not taken for the actual build step.
func indexOfCommandLine(dockerfile, substr string) int {
	offset := 0
	for _, line := range strings.SplitAfter(dockerfile, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") && strings.Contains(line, substr) {
			return offset
		}
		offset += len(line)
	}
	return -1
}

func indexOfCopyErunCommon(dockerfile string) int {
	offset := 0
	for _, line := range strings.SplitAfter(dockerfile, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "COPY erun-common") {
			return offset
		}
		offset += len(line)
	}
	return -1
}

func mustReadRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// The sentinel Dockerfile is the erun-devops image's own Dockerfile, which
// that image's test stage has no reason to COPY into its own build context
// (unlike erun-backend-api/Dockerfile, which the test stage now copies in for
// TestDockerfileLdflagsActuallyStampsTheBinary and so can no longer serve as
// this signal). Its absence from the partial in-build context is what marks
// that context as partial; its presence marks a full checkout.
func findFullCheckoutRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	const sentinel = "erun-devops/docker/erun-devops/Dockerfile"
	for {
		if _, err := os.Stat(filepath.Join(dir, sentinel)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
