// Package fixture writes deterministic seed configurations into an isolated
// HOME so erun subprocesses see a known tenant/environment/cloud-context layout
// without invoking interactive prompts.
package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// SeedGitRepo runs `git init` inside the given dir and creates one commit so
// commands that look at git state (release, diff, exec) can resolve a project
// root without any prompts.
func SeedGitRepo(t testing.TB, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := exec("git", []string{"init", "-q", "-b", "main"}, dir); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := exec("git", []string{"config", "user.email", "test@example"}, dir); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := exec("git", []string{"config", "user.name", "Test"}, dir); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	mustWrite(t, filepath.Join(dir, "README.md"), "# test\n")
	if err := exec("git", []string{"add", "."}, dir); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := exec("git", []string{"commit", "-q", "-m", "initial"}, dir); err != nil {
		t.Fatalf("git commit: %v", err)
	}
}

// SeedGitRepoBehindItsRemote makes dir a git checkout that is one commit
// behind the bare origin it tracks -- the state a machine that has not pulled
// since the last release is in, and the state a reported pin ran on: it read
// such a checkout and printed a plan that was internally consistent, with every
// site reading the old version and nothing anywhere saying the base was old.
func SeedGitRepoBehindItsRemote(t testing.TB, dir string) {
	t.Helper()
	SeedGitRepo(t, dir)
	origin := dir + "-origin.git"
	if err := exec("git", []string{"init", "-q", "--bare", "-b", "main", origin}, ""); err != nil {
		t.Fatalf("git init bare origin: %v", err)
	}
	for _, args := range [][]string{
		{"remote", "add", "origin", origin},
		{"push", "-q", "-u", "origin", "main"},
	} {
		if err := exec("git", args, dir); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	// The advance lands on the remote only: the checkout is rewound to what it
	// held before it, so the remote carries a commit this tree does not.
	mustWrite(t, filepath.Join(dir, "README.md"), "# test\n\nlanded after this checkout\n")
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-q", "-m", "advance the remote"},
		{"push", "-q", "origin", "main"},
		{"reset", "-q", "--hard", "HEAD~1"},
	} {
		if err := exec("git", args, dir); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
}

func exec(name string, args []string, dir string) error {
	cmd := harnessexec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Run()
}

func mustWrite(t testing.TB, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// SeedDeployInflightMarker writes an in-flight deploy marker into the test's
// XDG config dir so erun deploy --dry-run exercises the dedup branches
// (skip on identical hash, conflict on different hash, reclaim on dead PID).
// The marker filename mirrors erun-common's helmDeployReleaseKey format
// (<context>-<namespace>-<release>.json), so the sanitize logic here must track
// production's.
func SeedDeployInflightMarker(t testing.TB, setup env.Setup, kubernetesContext, namespace, releaseName string, record DeployInflightRecord) string {
	t.Helper()
	if record.StartedAt == "" {
		record.StartedAt = "2026-05-10T12:00:00Z"
	}
	dir := filepath.Join(setup.ConfigHome, "erun", "deploys")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir deploys dir: %v", err)
	}
	key := sanitizeFilename(kubernetesContext) + "-" + sanitizeFilename(namespace) + "-" + sanitizeFilename(releaseName)
	path := filepath.Join(dir, key+".json")
	body := fmt.Sprintf(`{
  "pid": %d,
  "started_at": %q,
  "params_hash": %q,
  "tenant": %q,
  "environment": %q,
  "version": %q
}
`, record.PID, record.StartedAt, record.ParamsHash, record.Tenant, record.Environment, record.Version)
	mustWrite(t, path, body)
	return path
}

// DeployInflightRecord captures the fields tests need to seed for the dedup
// integration scenarios.
type DeployInflightRecord struct {
	PID         int
	StartedAt   string
	ParamsHash  string
	Tenant      string
	Environment string
	Version     string
}

func sanitizeFilename(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
