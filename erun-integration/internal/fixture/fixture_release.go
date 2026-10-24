package fixture

import (
	"os"
	"path/filepath"
	"testing"
)

// SeedReleaseRepo materializes the minimal erun-devops layout and a git repo
// that release scenarios need as a project root.
func SeedReleaseRepo(t testing.TB, dir, branch string) string {
	t.Helper()
	releaseRoot := filepath.Join(dir, "erun-devops")
	for _, sub := range []string{
		filepath.Join(releaseRoot, "k8s", "api"),
		filepath.Join(releaseRoot, "k8s", "base"),
		filepath.Join(releaseRoot, "docker", "api"),
		filepath.Join(releaseRoot, "docker", "base"),
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	mustWrite(t, filepath.Join(releaseRoot, "VERSION"), "1.4.2\n")
	mustWrite(t, filepath.Join(releaseRoot, "k8s", "api", "Chart.yaml"), "apiVersion: v2\nname: api\nversion: 0.1.0\nappVersion: 0.1.0\n")
	// base is a version-pinned base: its image carries its own VERSION (9.9.9),
	// tagged and published at that version rather than the release's, same as
	// its co-located chart, which publishes at the release version so platform
	// deploys resolve it.
	mustWrite(t, filepath.Join(releaseRoot, "k8s", "base", "Chart.yaml"), "apiVersion: v2\nname: base\nversion: 0.1.0\nappVersion: 0.1.0\n")
	mustWrite(t, filepath.Join(releaseRoot, "docker", "api", "Dockerfile"), "FROM alpine:3.22\n")
	mustWrite(t, filepath.Join(releaseRoot, "docker", "base", "Dockerfile"), "FROM alpine:3.22\n")
	mustWrite(t, filepath.Join(releaseRoot, "docker", "base", "VERSION"), "9.9.9\n")

	if err := exec("git", []string{"init", "-q", "-b", branch}, dir); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := exec("git", []string{"config", "user.email", "test@example"}, dir); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := exec("git", []string{"config", "user.name", "Test"}, dir); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := exec("git", []string{"add", "."}, dir); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := exec("git", []string{"commit", "-q", "-m", "initial"}, dir); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return dir
}

// SeedTerraformModuleImageReference adds a third release image
// (erun-devops/docker/<imageName>) and a Terraform module under
// erun-devops/terraform-erun that references it the way
// terraform-erun-cluster-edge's real dns01_webhook_image references
// ghcr.io/sophium/erun-dns01-webhook: through an interpolated version, not a
// literal one. Release scenarios use this to exercise the
// anonymous-pullability check's module-reference discovery.
func SeedTerraformModuleImageReference(t testing.TB, dir, imageName string) {
	t.Helper()
	dockerDir := filepath.Join(dir, "erun-devops", "docker", imageName)
	if err := os.MkdirAll(dockerDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dockerDir, err)
	}
	mustWrite(t, filepath.Join(dockerDir, "Dockerfile"), "FROM alpine:3.22\n")

	moduleDir := filepath.Join(dir, "erun-devops", "terraform-erun", "modules", "terraform-erun-fixture")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", moduleDir, err)
	}
	mustWrite(t, filepath.Join(moduleDir, "main.tf"), `locals {
  fixture_image = local.arg_fixture_image != "" ? local.arg_fixture_image : "ghcr.io/sophium/`+imageName+`:${local.fixture_chart_app_version}"
}
`)
}

// SeedMarketplaceJSON writes a placeholder .claude-plugin/marketplace.json so
// release scenarios can exercise the marketplace.json source.sha bump path.
func SeedMarketplaceJSON(t testing.TB, dir string) {
	t.Helper()
	dst := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	mustWrite(t, filepath.Join(dst, "marketplace.json"), `{
  "name": "sophium/erun",
  "plugins": [
    {
      "name": "erun-tools",
      "source": {
        "source": "git-subdir",
        "url": "https://github.com/sophium/erun.git",
        "path": "erun-skills",
        "ref": "main",
        "sha": "0000000000000000000000000000000000000000"
      }
    }
  ]
}
`)
}

// SeedScoopManifest writes bucket/erun.json with the given content so stable
// release scenarios exercise the Scoop manifest validation and version/checksum
// sync paths; a valid manifest reaches the happy path, a malformed one drives
// the validation-failure branch.
func SeedScoopManifest(t testing.TB, dir, content string) {
	t.Helper()
	dst := filepath.Join(dir, "bucket")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	mustWrite(t, filepath.Join(dst, "erun.json"), content)
}

// SeedHomebrewFormula writes Formula/erun.rb so stable release scenarios
// exercise the Homebrew packaging path. The url/sha256 lines use the exact
// two-space indentation the production regexes anchor on — reformatting them
// breaks the match.
func SeedHomebrewFormula(t testing.TB, dir string) {
	t.Helper()
	dst := filepath.Join(dir, "Formula")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	mustWrite(t, filepath.Join(dst, "erun.rb"), `class Erun < Formula
  desc "erun developer toolkit"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.0.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "MIT"

  def install
    system "go", "build", "-trimpath", "-o", bin/"erun", "."
  end
end
`)
}

// ReleaseGitStubSpec configures StubReleaseGit, the argv-branching git stub for
// real-run release scenarios: every mutation is a silent no-op so the captured
// output stays deterministic without a real remote or network.
type ReleaseGitStubSpec struct {
	// Branch is returned for `rev-parse --abbrev-ref HEAD`.
	Branch string
	// ShortCommit is returned for `rev-parse --short HEAD`.
	ShortCommit string
	// TagSHA, when non-empty, is returned for the `<tag>^{}` tag-existence
	// probe and for `rev-parse HEAD`, so the release tag resolves to a
	// commit and (for the non-force path) that commit equals HEAD. Empty
	// makes the `^{}` probe exit 1: the tag does not exist yet.
	TagSHA string
	// RemoteTag, when non-empty, makes `ls-remote --tags` report the tag as
	// present on origin at TagSHA, so --force runs the remote tag deletion.
	// Empty reports no remote tag.
	RemoteTag string
}

// StubReleaseGit writes a git stub implementing spec and returns its
// ERUN_GIT_BIN routing pair. show-ref --verify --quiet always exits 1 (no
// develop branch) so stable releases skip the sync-develop stage and push only
// the main branch.
func StubReleaseGit(t testing.TB, stubsDir string, spec ReleaseGitStubSpec) []string {
	t.Helper()
	tagProbe := `exit 1`
	headResolve := `exit 1`
	if spec.TagSHA != "" {
		tagProbe = `echo ` + shellSingleQuote(spec.TagSHA)
		headResolve = `echo ` + shellSingleQuote(spec.TagSHA)
	}
	remoteTags := `:`
	if spec.RemoteTag != "" {
		remoteTags = `printf '%s\trefs/tags/%s\n' ` + shellSingleQuote(spec.TagSHA) + ` ` + shellSingleQuote(spec.RemoteTag)
	}
	StubBinaryWithScript(t, stubsDir, "git", `case "$*" in
  *'rev-parse --abbrev-ref HEAD'*) echo `+shellSingleQuote(spec.Branch)+` ;;
  *'rev-parse --short HEAD'*) echo `+shellSingleQuote(spec.ShortCommit)+` ;;
  *'ls-remote --tags'*) `+remoteTags+` ;;
  *'show-ref --verify --quiet'*) exit 1 ;;
  *'^{}'*) `+tagProbe+` ;;
  *'rev-parse HEAD'*) `+headResolve+` ;;
  *) : ;;
esac
exit 0
`)
	return StubEnv(stubsDir, "git")
}
