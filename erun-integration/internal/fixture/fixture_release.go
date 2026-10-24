package fixture

import (
	"os"
	"path/filepath"
	"testing"
)

// SeedReleaseRepo writes a minimal erun-devops layout in a git repo at dir.
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
	// base is version-pinned: its image publishes at its own VERSION (9.9.9),
	// while its chart publishes at the release version.
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

// SeedTerraformModuleImageReference adds a release image and a Terraform module
// that references it through an interpolated version, like
// terraform-erun-cluster-edge's dns01_webhook_image.
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

// SeedMarketplaceJSON writes a placeholder .claude-plugin/marketplace.json.
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

// SeedScoopManifest writes bucket/erun.json with content.
func SeedScoopManifest(t testing.TB, dir, content string) {
	t.Helper()
	dst := filepath.Join(dir, "bucket")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	mustWrite(t, filepath.Join(dst, "erun.json"), content)
}

// SeedHomebrewFormula writes Formula/erun.rb. The url/sha256 indentation is
// what the production regexes anchor on.
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

// ReleaseGitStubSpec configures StubReleaseGit; every git mutation is a no-op.
type ReleaseGitStubSpec struct {
	Branch      string
	ShortCommit string
	// TagSHA answers the `<tag>^{}` probe and `rev-parse HEAD`; empty means the
	// tag does not exist.
	TagSHA string
	// RemoteTag makes `ls-remote --tags` report the tag on origin at TagSHA.
	RemoteTag string
}

// StubReleaseGit writes a git stub implementing spec and returns its
// ERUN_GIT_BIN pair. show-ref always fails, so no develop branch exists.
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
