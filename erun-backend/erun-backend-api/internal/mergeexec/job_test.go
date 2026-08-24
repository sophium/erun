package mergeexec

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This file proves the property #1196 exists to establish, against the real
// script the Job runs — not a mock: two reviews that are each green alone can
// still be broken together, and the merge queue has to catch that before the
// target branch moves. It runs mergeScript's exact shell against real local
// git repositories, with a stub `erun` standing in for the real toolchain (the
// gate outcome this test cares about is the *merge*, not the build binary; the
// opt-in cluster e2e in erun-backend-api's root package exercises a real `erun
// build` gate against a real cluster).

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=erun-test", "GIT_AUTHOR_EMAIL=erun-test@example.com",
		"GIT_COMMITTER_NAME=erun-test", "GIT_COMMITTER_EMAIL=erun-test@example.com",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

// writeFile writes content and returns nothing; test helper for readability.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// stubToolchainPath writes a stub `erun` executable (a green `erun build`)
// into a fresh directory and returns that directory to prepend onto PATH. The
// merge queue's own outcome under test is the merge, not the build binary.
func stubToolchainPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	path := filepath.Join(dir, "erun")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub erun: %v", err)
	}
	return dir
}

// runMergeScript runs the exact script a merge-gate Job would run, in a fresh
// clone of origin, and reports whether it succeeded.
func runMergeScript(t *testing.T, originDir, toolchainPath string, params MergeJobParams) (succeeded bool, output string) {
	t.Helper()
	workDir := t.TempDir()
	runGit(t, workDir, "clone", originDir, ".")
	params.RepoPath = workDir

	cmd := exec.Command("sh", "-c", mergeScript(params))
	cmd.Env = append(os.Environ(),
		"PATH="+toolchainPath+":"+os.Getenv("PATH"),
		"GIT_AUTHOR_NAME=erun-test", "GIT_AUTHOR_EMAIL=erun-test@example.com",
		"GIT_COMMITTER_NAME=erun-test", "GIT_COMMITTER_EMAIL=erun-test@example.com",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return err == nil, out.String()
}

// TestMergeGateCatchesTwoReviewsThatAreGreenAloneButBrokenTogether is the test
// the issue calls the proof of the whole feature: two reviews, each of which
// merges cleanly onto main by itself, whose *combination* the target branch
// must never see. The first lands; the second is gated against the target the
// first left behind, fails, and main is left exactly where the first put it.
func TestMergeGateCatchesTwoReviewsThatAreGreenAloneButBrokenTogether(t *testing.T) {
	origin := seedOriginWithConflictingBranches(t)
	toolchain := stubToolchainPath(t)

	// Review A: green alone, lands on main.
	okA, outA := runMergeScript(t, origin, toolchain, MergeJobParams{
		TargetBranch: "main", SourceBranch: "feature/a", MergeMessage: "merge feature/a",
	})
	if !okA {
		t.Fatalf("review A (green alone) failed to merge:\n%s", outA)
	}
	mergeCommitA := lastMarker(outA, mergeCommitMarker)
	if mergeCommitA == "" {
		t.Fatalf("review A succeeded but reported no merge commit:\n%s", outA)
	}
	// main now carries A's change. Confirm it actually landed before testing B.
	assertCountGoContains(t, origin, `"a"`)

	// Review B: green alone against the ORIGINAL main, but never re-tested
	// against A — this is what a merge queue gates against the CURRENT target,
	// not a stale one.
	okB, outB := runMergeScript(t, origin, toolchain, MergeJobParams{
		TargetBranch: "main", SourceBranch: "feature/b", MergeMessage: "merge feature/b",
	})
	if okB {
		t.Fatalf("review B, which conflicts with A's already-landed change, was accepted:\n%s", outB)
	}
	if lastMarker(outB, mergeCommitMarker) != "" {
		t.Fatalf("a failed merge still reported a merge commit:\n%s", outB)
	}
	if lastMarker(outB, sourceCommitMarker) == "" {
		t.Fatalf("a failed merge reported no source commit to record the failure against:\n%s", outB)
	}

	// The property under test: main is exactly where A left it. B's failure
	// must never have moved the target branch.
	assertMainAtCommit(t, origin, mergeCommitA)
	assertCountGoContains(t, origin, `"a"`)
	assertCountGoDoesNotContain(t, origin, `"b"`)
}

// seedOriginWithConflictingBranches sets up a bare origin with main seeded at
// count.go="start", plus two branches (feature/a, feature/b) forked from that
// same main, each independently setting count.go's value — fine alone,
// conflicting once one has already landed on main.
func seedOriginWithConflictingBranches(t *testing.T) string {
	t.Helper()
	origin := t.TempDir()
	runGit(t, origin, "init", "--bare", "-b", "main", ".")

	seed := t.TempDir()
	runGit(t, seed, "init", "-b", "main", ".")
	writeFile(t, filepath.Join(seed, "count.go"), "package count\n\nconst Value = \"start\"\n")
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-m", "seed")
	runGit(t, seed, "remote", "add", "origin", origin)
	runGit(t, seed, "push", "origin", "main")

	for _, branch := range []struct{ name, value string }{{"feature/a", "a"}, {"feature/b", "b"}} {
		work := t.TempDir()
		runGit(t, work, "clone", origin, ".")
		runGit(t, work, "checkout", "-b", branch.name)
		writeFile(t, filepath.Join(work, "count.go"), "package count\n\nconst Value = \""+branch.value+"\"\n")
		runGit(t, work, "commit", "-am", "set value to "+branch.value)
		runGit(t, work, "push", "origin", branch.name)
	}
	return origin
}

func assertMainAtCommit(t *testing.T, origin, wantCommit string) {
	t.Helper()
	clone := t.TempDir()
	runGit(t, clone, "clone", origin, ".")
	got := strings.TrimSpace(runGit(t, clone, "rev-parse", "main"))
	if got != wantCommit {
		t.Fatalf("main is at %s, want %s", got, wantCommit)
	}
}

func assertCountGoContains(t *testing.T, origin, want string) {
	t.Helper()
	clone := t.TempDir()
	runGit(t, clone, "clone", origin, ".")
	content, err := os.ReadFile(filepath.Join(clone, "count.go"))
	if err != nil || !strings.Contains(string(content), want) {
		t.Fatalf("main's count.go = %v %q, want it to contain %q", err, content, want)
	}
}

func assertCountGoDoesNotContain(t *testing.T, origin, absent string) {
	t.Helper()
	clone := t.TempDir()
	runGit(t, clone, "clone", origin, ".")
	content, err := os.ReadFile(filepath.Join(clone, "count.go"))
	if err != nil {
		t.Fatalf("read count.go: %v", err)
	}
	if strings.Contains(string(content), absent) {
		t.Fatalf("main's count.go = %q, want it to NOT contain %q", content, absent)
	}
}

// TestMergeGateLandsTwoNonConflictingReviewsSequentially is the companion
// case: two reviews that do NOT conflict must both land, each gated against
// the target the previous one left behind (main after A contains A's change
// by the time B is gated, and B's own diff still applies cleanly on top of it).
func TestMergeGateLandsTwoNonConflictingReviewsSequentially(t *testing.T) {
	origin := t.TempDir()
	runGit(t, origin, "init", "--bare", "-b", "main", ".")

	seed := t.TempDir()
	runGit(t, seed, "init", "-b", "main", ".")
	writeFile(t, filepath.Join(seed, "a.txt"), "a\n")
	writeFile(t, filepath.Join(seed, "b.txt"), "b\n")
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-m", "seed")
	runGit(t, seed, "remote", "add", "origin", origin)
	runGit(t, seed, "push", "origin", "main")

	for _, branch := range []struct{ name, file, content string }{
		{"feature/a", "a.txt", "a-changed\n"},
		{"feature/b", "b.txt", "b-changed\n"},
	} {
		work := t.TempDir()
		runGit(t, work, "clone", origin, ".")
		runGit(t, work, "checkout", "-b", branch.name)
		writeFile(t, filepath.Join(work, branch.file), branch.content)
		runGit(t, work, "commit", "-am", "change "+branch.file)
		runGit(t, work, "push", "origin", branch.name)
	}

	toolchain := stubToolchainPath(t)

	okA, outA := runMergeScript(t, origin, toolchain, MergeJobParams{TargetBranch: "main", SourceBranch: "feature/a", MergeMessage: "merge feature/a"})
	if !okA {
		t.Fatalf("review A failed to merge:\n%s", outA)
	}
	okB, outB := runMergeScript(t, origin, toolchain, MergeJobParams{TargetBranch: "main", SourceBranch: "feature/b", MergeMessage: "merge feature/b"})
	if !okB {
		t.Fatalf("review B, which does not conflict with A, failed to merge against the post-A target:\n%s", outB)
	}
	if lastMarker(outB, mergeCommitMarker) == "" {
		t.Fatalf("review B succeeded but reported no merge commit:\n%s", outB)
	}

	final := t.TempDir()
	runGit(t, final, "clone", origin, ".")
	a, _ := os.ReadFile(filepath.Join(final, "a.txt"))
	b, _ := os.ReadFile(filepath.Join(final, "b.txt"))
	if strings.TrimSpace(string(a)) != "a-changed" || strings.TrimSpace(string(b)) != "b-changed" {
		t.Fatalf("main after both merges = a:%q b:%q, want both changes landed", a, b)
	}
	// B's gate build ran against a commit that contains A: the second review's
	// merge base is main, which already carries A.
	log := runGit(t, final, "log", "--oneline", "main")
	if strings.Count(log, "\n") < 2 {
		t.Fatalf("main history = %q, want at least seed + A + B", log)
	}
}
