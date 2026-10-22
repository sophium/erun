package eruncommon

import "testing"

// TestRepositorySlugReducesAForgeIdentityToItsOwnerAndRepository covers the
// identities RepositoryIdentity actually produces for a GitHub-style forge,
// plus the spellings that name no owner/repo pair at all.
func TestRepositorySlugReducesAForgeIdentityToItsOwnerAndRepository(t *testing.T) {
	cases := []struct {
		name     string
		identity string
		want     string
		wantOK   bool
	}{
		{name: "https remote", identity: "https://github.com/sophium/erun", want: "sophium/erun", wantOK: true},
		{name: "https remote with a port", identity: "https://github.example.com:8443/sophium/erun", want: "sophium/erun", wantOK: true},
		{name: "git scheme", identity: "git://github.com/sophium/erun", want: "sophium/erun", wantOK: true},
		{name: "an ssh remote on a port keeps its own spelling", identity: "ssh://git@github.com:2222/sophium/erun", want: "sophium/erun", wantOK: true},
		// A file remote and a bare local path name no forge, so there is no
		// owner/repo to read out of them.
		{name: "file remote", identity: "file:///tmp/erun"},
		{name: "bare local path", identity: "/tmp/erun"},
		// A deeper path leaves it ambiguous which segments are the owner and
		// the repository; guessing is the failure this refuses.
		{name: "nested path", identity: "https://gitlab.com/group/sub/repo"},
		{name: "host only", identity: "https://github.com"},
		{name: "empty", identity: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RepositorySlug(tc.identity)
			if ok != tc.wantOK {
				t.Fatalf("RepositorySlug(%q) ok = %v, want %v (slug %q)", tc.identity, ok, tc.wantOK, got)
			}
			if got != tc.want {
				t.Fatalf("RepositorySlug(%q) = %q, want %q", tc.identity, got, tc.want)
			}
		})
	}
}

// TestCanonicalIssueKeyJoinsARecordedRepositoryToABareNumber is the union's
// whole join: a review's inferred link is a bare number, and the key a job
// can be matched against is that number addressed to the review's own
// repository.
func TestCanonicalIssueKeyJoinsARecordedRepositoryToABareNumber(t *testing.T) {
	key, ok := CanonicalIssueKey("2683", "https://github.com/sophium/erun")
	if !ok {
		t.Fatal("CanonicalIssueKey reported no key for a bare number with a recorded repository")
	}
	if key != "sophium/erun#2683" {
		t.Fatalf("CanonicalIssueKey = %q, want %q", key, "sophium/erun#2683")
	}
}

// TestCanonicalIssueKeyLeavesAnAlreadyCanonicalReferenceAlone: a reference
// that names its own repository is not re-derived from the work item's own,
// because the two disagreeing is a deliberate reassignment the platform has
// no business overruling.
func TestCanonicalIssueKeyLeavesAnAlreadyCanonicalReferenceAlone(t *testing.T) {
	key, ok := CanonicalIssueKey("sophium/erun#2683", "https://github.com/other/repo")
	if !ok {
		t.Fatal("CanonicalIssueKey refused a canonical reference")
	}
	if key != "sophium/erun#2683" {
		t.Fatalf("CanonicalIssueKey = %q, want the reference verbatim", key)
	}
}

// TestCanonicalIssueKeyReportsNoKeyRatherThanGuessing: every shape with no
// honest key answers false, which the view renders as work that names no
// issue.
func TestCanonicalIssueKeyReportsNoKeyRatherThanGuessing(t *testing.T) {
	cases := []struct {
		name       string
		ref        string
		repository string
	}{
		{name: "no reference", ref: "", repository: "https://github.com/sophium/erun"},
		{name: "bare number with no repository", ref: "2683"},
		{name: "bare number with an unreadable repository", ref: "2683", repository: "file:///tmp/erun"},
		{name: "prose", ref: "the jobs issue", repository: "https://github.com/sophium/erun"},
		{name: "a branch slug", ref: "2683-add-planned-jobs", repository: "https://github.com/sophium/erun"},
		{name: "a number in an unusable key", ref: "sophium/erun#", repository: "https://github.com/sophium/erun"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, ok := CanonicalIssueKey(tc.ref, tc.repository)
			if ok {
				t.Fatalf("CanonicalIssueKey(%q, %q) = %q, want no key", tc.ref, tc.repository, key)
			}
		})
	}
}

// TestNormalizeIssueRefCanonicalizesWhatAReviewRecords pins the two accepted
// spellings and the refusals, since this is the trust boundary a declared
// reference arrives through.
func TestNormalizeIssueRefCanonicalizesWhatAReviewRecords(t *testing.T) {
	cases := []struct {
		name       string
		declared   string
		repository string
		want       string
		wantErr    bool
	}{
		{name: "absent", declared: "  ", repository: "https://github.com/sophium/erun"},
		{name: "canonical verbatim", declared: "sophium/erun#2683", repository: "https://github.com/other/repo", want: "sophium/erun#2683"},
		{name: "bare number joined to the repository", declared: "2683", repository: "https://github.com/sophium/erun", want: "sophium/erun#2683"},
		{name: "bare number with no repository", declared: "2683", repository: "file:///tmp/erun", wantErr: true},
		{name: "prose", declared: "the jobs issue", repository: "https://github.com/sophium/erun", wantErr: true},
		{name: "a number with no hash separator", declared: "sophium/erun 2683", repository: "https://github.com/sophium/erun", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeIssueRef(tc.declared, tc.repository)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeIssueRef(%q) = %q, want a refusal", tc.declared, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeIssueRef(%q) failed: %v", tc.declared, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeIssueRef(%q) = %q, want %q", tc.declared, got, tc.want)
			}
		})
	}
}
