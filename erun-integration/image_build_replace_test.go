package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImageDockerfilesCopyLocalReplaceTarget guards the regression in #691: a
// module whose go.mod locally replaces erun-common (`replace … => ../../erun-common`)
// can only build when the replace target is present in the Docker build context.
// erun-backend-api's image Dockerfile copied only the module's own go.mod/go.sum,
// so a cold (fingerprint-cache-miss) build failed at `go mod download` reading the
// missing ../../erun-common — surfacing only on a fresh release version. The
// invariant: every erun-devops image Dockerfile that builds an erun-common-replacing
// module must COPY erun-common before its first `go mod download`.
func TestImageDockerfilesCopyLocalReplaceTarget(t *testing.T) {
	// The integration suite also runs inside the erun-devops image build, whose
	// context copies only the Go modules (erun-cli/common/mcp/integration) — not
	// erun-backend/ or erun-devops/docker/. This static Dockerfile guard needs the
	// full source tree, so it no-ops in that partial context and runs on a full
	// checkout (where a developer/CI runs the gate before a release).
	root, ok := findFullCheckoutRoot()
	if !ok {
		t.Skip("full source tree not present (partial in-build build context); this Dockerfile-content guard runs on a full checkout")
	}

	cases := []struct {
		name       string
		goMod      string // module whose go.mod is checked for the local replace
		dockerfile string // image Dockerfile that builds it
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

// indexOfCommandLine returns the byte offset of the first non-comment line
// containing substr (so a comment that merely mentions `go mod download` is not
// mistaken for the build step itself), or -1 if absent.
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

// indexOfCopyErunCommon returns the byte offset of the first `COPY erun-common…`
// line (matching both `COPY erun-common/go.mod …` and `COPY erun-common /dest`),
// or -1 if absent.
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

// findFullCheckoutRoot walks up from the working directory looking for the
// erun-backend-api image Dockerfile — a file present only in a full checkout,
// not in the partial erun-devops in-build context. Returns (root, true) when
// found, ("", false) otherwise so callers can skip.
func findFullCheckoutRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	const sentinel = "erun-devops/docker/erun-backend-api/Dockerfile"
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
