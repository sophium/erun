package shared

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

func releasePushTestSpec() model.ReleaseSpec {
	return model.ReleaseSpec{
		ProjectRoot: "/repo",
		Branch:      "main",
		Version:     "1.4.2",
	}
}

func TestParseReleasePushRejections(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []ReleasePushRejection
	}{
		{
			name:   "several refs rejected at once",
			output: " ! [rejected]        main -> main (fetch first)\n ! [rejected]        develop -> develop (non-fast-forward)\n",
			want: []ReleasePushRejection{
				{Ref: "main", Reason: "fetch first"},
				{Ref: "develop", Reason: "non-fast-forward"},
			},
		},
		{
			name:   "a rejection with no reason is still named",
			output: " ! [rejected]        develop -> develop\n",
			want:   []ReleasePushRejection{{Ref: "develop"}},
		},
		{
			name:   "output with no rejection at all",
			output: "   46e01916..ae29151f  main -> main\n",
			want:   nil,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ParseReleasePushRejections(testCase.output)
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

func TestReleasePushRejectedErrorNamesTheHalfReleaseAndNeverThePrePublicationShape(t *testing.T) {
	publishedSpec := releasePushTestSpec()
	publishedSpec.ArtifactsPublished = true
	cases := []struct {
		name       string
		rejections []ReleasePushRejection
		wantIn     []string
		notIn      []string
	}{
		{
			name:       "a develop rejection",
			rejections: []ReleasePushRejection{{Ref: "develop", Reason: "non-fast-forward"}},
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
			// An empty ref would make the recovery command unrunnable.
			notIn: []string{"reconcile  with its remote", "is  ."},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ReleasePushRejectedError(publishedSpec, testCase.rejections, errors.New("exit status 1"))
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

// A source-control-only release must not claim artifacts were published; its recovery must
// name the publish it still owes, since `erun deploy` never builds.
func TestReleasePushRejectedErrorDoesNotClaimArtifactsASourceControlOnlyReleaseNeverPublished(t *testing.T) {
	spec := releasePushTestSpec() // ArtifactsPublished stays false: `erun release`.
	message := ReleasePushRejectedError(spec, []ReleasePushRejection{{Ref: "develop", Reason: "non-fast-forward"}}, errors.New("exit status 1")).Error()

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

	recoveries := []struct {
		name    string
		message string
	}{
		{"the rebase that could not absorb the move", ReleaseRebaseFailedRecovery(spec, "main")},
		{"the tag that could not be re-pointed", ReleaseRepointFailedRecovery(spec)},
	}
	for _, recovery := range recoveries {
		assertMessageOmits(t, recovery.message, "already published")
		assertMessageNames(t, recovery.message, "erun push --version 1.4.2")
	}
}

func TestReleasePushRejectedErrorKeepsTheRegistryClaimForARunThatPublished(t *testing.T) {
	spec := releasePushTestSpec()
	spec.ArtifactsPublished = true

	message := ReleasePushRejectedError(spec, []ReleasePushRejection{{Ref: "develop", Reason: "non-fast-forward"}}, errors.New("exit status 1")).Error()
	assertMessageNames(t, message, "images and charts verified on the registry")
	assertMessageOmits(t, message, "erun push --version")

	for _, recovery := range []string{
		ReleaseRebaseFailedRecovery(spec, "main"),
		ReleaseRepointFailedRecovery(spec),
	} {
		assertMessageNames(t, recovery, "is already published")
		assertMessageOmits(t, recovery, "erun push --version")
	}
}

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
			got := ReleasePushRejectedTheMovedBaseBranch(ParseReleasePushRejections(testCase.output), testCase.branch)
			if got != testCase.wantRebase {
				t.Fatalf("got %v, want %v (%s)", got, testCase.wantRebase, testCase.why)
			}
		})
	}
}
