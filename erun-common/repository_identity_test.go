package eruncommon

import "testing"

func TestRepositoryIdentityFoldsEverySpellingOfOneRepository(t *testing.T) {
	for _, tc := range []struct {
		name  string
		given string
		want  string
	}{
		{"scp-like, as git remote get-url origin writes it over ssh", "git@github.com:sophium/erun.git", "https://github.com/sophium/erun"},
		{"scp-like without a user", "github.com:sophium/erun.git", "https://github.com/sophium/erun"},
		{"ssh url with a user", "ssh://git@github.com/sophium/erun.git", "https://github.com/sophium/erun"},
		{"ssh url without a user", "ssh://github.com/sophium/erun.git", "https://github.com/sophium/erun"},
		{"https with the .git suffix", "https://github.com/sophium/erun.git", "https://github.com/sophium/erun"},
		{"https without it", "https://github.com/sophium/erun", "https://github.com/sophium/erun"},
		{"a trailing slash", "https://github.com/sophium/erun/", "https://github.com/sophium/erun"},
		{"surrounding space", "  git@github.com:sophium/erun.git  ", "https://github.com/sophium/erun"},
		{"a host, which is not case-sensitive", "https://GitHub.com/sophium/erun.git", "https://github.com/sophium/erun"},
		{"the scheme, which is not either", "HTTPS://github.com/sophium/erun", "https://github.com/sophium/erun"},
		{"a path, which may be", "https://github.com/Sophium/Erun.git", "https://github.com/Sophium/Erun"},
		{"a local file remote", "file:///srv/git/erun.git", "file:///srv/git/erun"},
		{"a bare local path", "/srv/git/erun.git", "/srv/git/erun"},
		// An ssh remote on a port of its own has no HTTPS form to be named
		// by, so it keeps its own spelling rather than collapsing onto a
		// repository it may not be.
		{"ssh on its own port", "ssh://git@github.com:2222/sophium/erun.git", "ssh://github.com:2222/sophium/erun"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RepositoryIdentity(tc.given)
			if err != nil {
				t.Fatalf("RepositoryIdentity(%q): %v", tc.given, err)
			}
			if got != tc.want {
				t.Fatalf("RepositoryIdentity(%q) = %q, want %q", tc.given, got, tc.want)
			}
		})
	}
}

// TestRepositoryIdentityAgreesAcrossTheFormsAClientMayHold is the property the
// review's repository identity exists for: a review created from an SSH
// checkout must be the one the environment driving the merge queue finds from
// the HTTPS remote it reads.
func TestRepositoryIdentityAgreesAcrossTheFormsAClientMayHold(t *testing.T) {
	ssh, err := RepositoryIdentity("git@github.com:sophium/erun.git")
	if err != nil {
		t.Fatalf("RepositoryIdentity over ssh: %v", err)
	}
	https, err := RepositoryIdentity("https://github.com/sophium/erun")
	if err != nil {
		t.Fatalf("RepositoryIdentity over https: %v", err)
	}
	if ssh != https {
		t.Fatalf("the ssh and https spellings of one repository answered %q and %q; a review created from one would not be found by the other", ssh, https)
	}
}

func TestRepositoryIdentityRefusesARemoteThatNamesNoRepository(t *testing.T) {
	for _, given := range []string{
		"", "   ", "/", "https://github.com/", "https://github.com",
		// A shorthand names a repository on whichever forge the caller had in
		// mind, and a relative path names a place in the caller's own working
		// tree: each is a second identity for a repository the platform
		// already records under its remote, so a filter naming one answered a
		// silent subset of that repository's reviews rather than refusing.
		"sophium/erun", "sophium/erun.git", "github.com/sophium/erun",
		"../erun", "./erun", "erun",
	} {
		t.Run(given, func(t *testing.T) {
			if got, err := RepositoryIdentity(given); err == nil {
				t.Fatalf("RepositoryIdentity(%q) = %q with no error; a review cannot record an identity that names no repository", given, got)
			}
		})
	}
}

// The forms that do name one repository are the ones erun's own clients hold:
// `git remote get-url origin` answers an SSH remote, an HTTPS one, a file URL,
// or (for a repository on local disk) an absolute path.
func TestRepositoryIdentityAcceptsEveryFormARemoteIsHeldIn(t *testing.T) {
	for _, given := range []string{
		"git@github.com:sophium/erun.git",
		"https://github.com/sophium/erun",
		"file:///srv/git/erun.git",
		"/srv/git/erun.git",
		`C:\src\erun.git`,
	} {
		t.Run(given, func(t *testing.T) {
			if _, err := RepositoryIdentity(given); err != nil {
				t.Fatalf("RepositoryIdentity(%q): %v", given, err)
			}
		})
	}
}
