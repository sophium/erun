package eruncommon

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// The two directions of the misdiagnosed release push, as unit tests over the
// push itself.
//
// The release pushes main, develop and the tag in one command. Only a base
// branch that moved is repaired by rebasing the base branch; a develop rejected
// as a non-fast-forward is a different failure, and rebasing main onto an
// origin/main that never moved spends every retry without ever touching the ref
// git refused.

// developRejection is the 1.0.258 shape: main pushed, develop was rejected as a
// non-fast-forward because another release had advanced origin/develop while
// this release's own sync-develop added commits locally.
const developRejection = "To github.com:sophium/erun.git\n" +
	"   46e01916..ae29151f  main -> main\n" +
	" ! [rejected]        develop -> develop (non-fast-forward)\n" +
	"error: failed to push some refs to 'github.com:sophium/erun.git'\n"

const movedMainRejection = "To github.com:sophium/erun.git\n" +
	" * [new tag]         v1.4.2 -> v1.4.2\n" +
	" ! [rejected]        main -> main (fetch first)\n" +
	"error: failed to push some refs to 'github.com:sophium/erun.git'\n"

// recordingReleasePushRunner stands in for git's push. It fails the first push
// with the given rejection, then succeeds, recording every invocation so a test
// can assert which remediation ran — the point is that the wrong
// one did.
func recordingReleasePushRunner(rejection string) (GitCommandRunnerFunc, *[][]string) {
	calls := &[][]string{}
	pushed := false
	runner := func(root string, stdout, stderr io.Writer, args ...string) error {
		*calls = append(*calls, append([]string{}, args...))
		if args[0] != "push" {
			return nil
		}
		if pushed {
			return nil
		}
		pushed = true
		_, _ = fmt.Fprint(stderr, rejection)
		return errors.New("exit status 1")
	}
	return runner, calls
}

func releasePushTestSpec() ReleaseSpec {
	return ReleaseSpec{
		ProjectRoot: "/repo",
		Branch:      "main",
		Version:     "1.4.2",
	}
}

func releasePushTestCommand(dir string) ReleaseCommandSpec {
	return ReleaseCommandSpec{
		Dir:  dir,
		Name: "git",
		Args: []string{"push", "--follow-tags", "origin", "main", "develop"},
	}
}

func releasePushTestContext() Context {
	return Context{Stdout: io.Discard, Stderr: io.Discard}
}

func TestReleasePushRejectionOfDevelopIsNotAttributedToMainHavingMoved(t *testing.T) {
	runGit, calls := recordingReleasePushRunner(developRejection)

	err := runReleaseBranchPush(releasePushTestContext(), releasePushTestSpec(), releasePushTestCommand(t.TempDir()), runGit)
	if err == nil {
		t.Fatal("a rejected develop must fail the release")
	}

	message := err.Error()
	if !strings.Contains(message, "develop") || !strings.Contains(message, "non-fast-forward") {
		t.Fatalf("the error must name the rejected ref and git's own reason, got:\n%s", message)
	}
	if strings.Contains(message, "origin/main moved during the release") {
		t.Fatalf("a develop rejection must not be attributed to origin/main having moved, got:\n%s", message)
	}
	// The silent half: the error has to say what is still missing rather
	// than read as the pre-publication shape whose recovery would delete a
	// public tag.
	if !strings.Contains(message, "GitHub Release") || !strings.Contains(message, "do not delete tag v1.4.2") {
		t.Fatalf("the error must name the missing GitHub Release object and warn against deleting the tag, got:\n%s", message)
	}

	assertNoUnrequestedRemediation(t, *calls)
}

// assertNoUnrequestedRemediation pins the other half of the develop case: the
// rejected ref was develop, so no fetch, rebase, or retry may run at all.
func assertNoUnrequestedRemediation(t *testing.T, calls [][]string) {
	t.Helper()
	for _, call := range calls {
		if call[0] == "rebase" || call[0] == "fetch" {
			t.Fatalf("a develop rejection must not rebase or fetch, ran %v", call)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("a develop rejection must not be retried, ran %d git commands: %v", len(calls), calls)
	}
}

func TestReleasePushRetryRebasesTheBaseBranchThatActuallyMoved(t *testing.T) {
	runGit, calls := recordingReleasePushRunner(movedMainRejection)

	if err := runReleaseBranchPush(releasePushTestContext(), releasePushTestSpec(), releasePushTestCommand(t.TempDir()), runGit); err != nil {
		t.Fatalf("a base branch that moved must still be absorbed, got: %v", err)
	}

	var rebased, repushed bool
	for _, call := range *calls {
		switch call[0] {
		case "fetch", "rebase":
			if call[0] == "rebase" {
				rebased = true
			}
		case "push":
			repushed = true
		}
	}
	if !rebased {
		t.Fatalf("a moved base branch must be rebased onto, ran %v", *calls)
	}
	if !repushed || len(*calls) < 3 {
		t.Fatalf("the push must be retried after the rebase, ran %v", *calls)
	}
}

func TestParseReleasePushRejections(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []releasePushRejection
	}{
		{
			name:   "develop non-fast-forward alongside a landed main",
			output: developRejection,
			want:   []releasePushRejection{{Ref: "develop", Reason: "non-fast-forward"}},
		},
		{
			name:   "a base branch rejected because the remote moved first",
			output: movedMainRejection,
			want:   []releasePushRejection{{Ref: "main", Reason: "fetch first"}},
		},
		{
			name:   "several refs rejected at once",
			output: " ! [rejected]        main -> main (fetch first)\n ! [rejected]        develop -> develop (non-fast-forward)\n",
			want: []releasePushRejection{
				{Ref: "main", Reason: "fetch first"},
				{Ref: "develop", Reason: "non-fast-forward"},
			},
		},
		{
			name:   "a rejection with no reason is still named",
			output: " ! [rejected]        develop -> develop\n",
			want:   []releasePushRejection{{Ref: "develop"}},
		},
		{
			name:   "output with no rejection at all",
			output: "   46e01916..ae29151f  main -> main\n",
			want:   nil,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := parseReleasePushRejections(testCase.output)
			if len(got) != len(testCase.want) {
				t.Fatalf("got %v, want %v", got, testCase.want)
			}
			for i, want := range testCase.want {
				if got[i] != want {
					t.Fatalf("rejection %d: got %+v, want %+v", i, got[i], want)
				}
			}
		})
	}
}

// The half-release that must never read as an interrupted release: the version
// is already public, so the recovery is to name the missing GitHub Release
// object and the ref that did not land, never to delete the tag and reset a
// branch that already landed.
func TestReleasePushRejectedErrorNamesTheHalfReleaseAndNeverThePrePublicationShape(t *testing.T) {
	// This is the run that published: `erun build --release` reaches the push
	// with its images and charts verified on the registry.
	publishedSpec := releasePushTestSpec()
	publishedSpec.ArtifactsPublished = true
	cases := []struct {
		name       string
		rejections []releasePushRejection
		wantIn     []string
		notIn      []string
	}{
		{
			name:       "a develop rejection",
			rejections: []releasePushRejection{{Ref: "develop", Reason: "non-fast-forward"}},
			wantIn: []string{
				"develop (non-fast-forward)",
				"reconcile develop with its remote",
				"GitHub Release",
				"do not delete tag v1.4.2",
			},
			notIn: []string{
				"origin/main moved during the release",
				"reconcile develop (non-fast-forward) with its remote",
			},
		},
		{
			name:       "a rejection git did not name",
			rejections: nil,
			wantIn: []string{
				"a ref git did not name",
				"reconcile a ref git did not name with its remote",
				"GitHub Release",
			},
			// The bare-instruction slot must never be filled with nothing: an
			// empty ref makes the recovery command unrunnable.
			notIn: []string{"reconcile  with its remote", "is  ."},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := releasePushRejectedError(publishedSpec, testCase.rejections, errors.New("exit status 1"))
			message := err.Error()
			for _, want := range testCase.wantIn {
				if !strings.Contains(message, want) {
					t.Fatalf("error must contain %q, got:\n%s", want, message)
				}
			}
			for _, unwanted := range testCase.notIn {
				if strings.Contains(message, unwanted) {
					t.Fatalf("error must not contain %q, got:\n%s", unwanted, message)
				}
			}
		})
	}
}

// The reported failure: a release that never built or published anything failed
// its final push and reported "its images and charts verified on the registry".
// Nothing in that run wrote to a registry — its own dry run says "source control
// only; no artifacts were built or published", `erun release --help` states the
// same contract, and the stage that would have produced an artifact runs after
// the one that failed.
//
// The cost is not the wording. The message is read at the moment an operator is
// deciding what is left to do, and it answered "only develop and the GitHub
// Release object" when the entire artifact set was missing, so the publish that
// version still needed was the one step it told them to skip. A tag with no
// artifacts behind it is a version no environment can deploy: `erun deploy`
// installs by reference and never builds.
func TestReleasePushRejectedErrorDoesNotClaimArtifactsASourceControlOnlyReleaseNeverPublished(t *testing.T) {
	spec := releasePushTestSpec() // ArtifactsPublished stays false: `erun release`.
	message := releasePushRejectedError(spec, []releasePushRejection{{Ref: "develop", Reason: "non-fast-forward"}}, errors.New("exit status 1")).Error()

	assertMessageOmits(t, message,
		"images and charts verified on the registry",
		"Everything else this release publishes is already public",
	)
	assertMessageNames(t, message,
		"source control only",
		"version 1.4.2 is on no registry",
		"erun push --version 1.4.2",
		"develop (non-fast-forward)",
		"do not delete tag v1.4.2",
	)

	// The same claim is made by the push's two deep recoveries, and they carry
	// the same obligation: name the publish this run still owes.
	recoveries := []struct {
		name    string
		message string
	}{
		{"the rebase that could not absorb the move", releaseRebaseFailedRecovery(spec, "main")},
		{"the tag that could not be re-pointed", releaseRepointFailedRecovery(spec)},
	}
	for _, recovery := range recoveries {
		assertMessageOmits(t, recovery.message, "already published")
		assertMessageNames(t, recovery.message, "erun push --version 1.4.2")
	}
}

// And the run that did publish keeps the claim, because for it the claim is
// true: this is the release's own accounting, not a hedge in every message.
func TestReleasePushRejectedErrorKeepsTheRegistryClaimForARunThatPublished(t *testing.T) {
	spec := releasePushTestSpec()
	spec.ArtifactsPublished = true

	message := releasePushRejectedError(spec, []releasePushRejection{{Ref: "develop", Reason: "non-fast-forward"}}, errors.New("exit status 1")).Error()
	assertMessageNames(t, message, "images and charts verified on the registry")
	assertMessageOmits(t, message, "erun push --version")

	for _, recovery := range []string{
		releaseRebaseFailedRecovery(spec, "main"),
		releaseRepointFailedRecovery(spec),
	} {
		assertMessageNames(t, recovery, "is already published")
		assertMessageOmits(t, recovery, "erun push --version")
	}
}

// assertMessageNames and assertMessageOmits keep the per-string loops out of
// the tests that make several of these claims at once, so a case's list of
// expectations reads as the claim rather than as iteration.
func assertMessageNames(t *testing.T, message string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(message, want) {
			t.Fatalf("message must contain %q, got:\n%s", want, message)
		}
	}
}

func assertMessageOmits(t *testing.T, message string, unwanted ...string) {
	t.Helper()
	for _, omit := range unwanted {
		if strings.Contains(message, omit) {
			t.Fatalf("message must not contain %q, got:\n%s", omit, message)
		}
	}
}

func TestReleasePushRejectedTheMovedBaseBranch(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		branch     string
		wantRebase bool
		why        string
	}{
		{
			name:       "the base branch moved",
			output:     movedMainRejection,
			branch:     "main",
			wantRebase: true,
		},
		{
			name:   "develop diverged while main was never rejected",
			output: developRejection,
			branch: "main",
			why:    "rebasing main onto an unmoved origin/main is a no-op",
		},
		{
			name:   "the base branch was rejected together with another ref",
			output: " ! [rejected]        main -> main (fetch first)\n ! [rejected]        develop -> develop (non-fast-forward)\n",
			branch: "main",
			why:    "rebasing main cannot repair the develop divergence the same push reported",
		},
		{
			name:   "the base branch was rejected by something other than a move",
			output: " ! [rejected]        main -> main (protected branch hook declined)\n",
			branch: "main",
			why:    "a hook is not a branch that moved",
		},
		{
			name:   "no rejection could be read",
			output: "error: failed to push some refs\n",
			branch: "main",
			why:    "an unreadable rejection must not be guessed at",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := releasePushRejectedTheMovedBaseBranch(parseReleasePushRejections(testCase.output), testCase.branch)
			if got != testCase.wantRebase {
				t.Fatalf("got %v, want %v (%s)", got, testCase.wantRebase, testCase.why)
			}
		})
	}
}
