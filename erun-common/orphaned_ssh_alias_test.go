package eruncommon

import "testing"

func TestParseSSHHostEntriesReadsTheDirectivesErunWrites(t *testing.T) {
	content := `# a hand-maintained header
Host erun-team-dev
  HostName 127.0.0.1
  Port 17022
  User erun
  HostKeyAlias erun-team-dev
  IdentityFile /home/op/.ssh/id_ed25519
  ForwardAgent yes

Host erun-team-gone erun-team-also-gone
  Port 17122
`
	want := []SSHHostEntry{
		{Alias: "erun-team-dev", HostKeyAlias: "erun-team-dev", HostName: "127.0.0.1", Port: 17022, User: "erun", IdentityFile: "/home/op/.ssh/id_ed25519"},
		{Alias: "erun-team-gone", Port: 17122},
		{Alias: "erun-team-also-gone", Port: 17122},
	}
	got := ParseSSHHostEntries(content)
	if len(got) != len(want) {
		t.Fatalf("a shared Host line must yield one entry per alias, got %d: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d misparsed:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestParseSSHHostEntriesIgnoresNegatedNamesAndBareDirectives(t *testing.T) {
	content := `Host erun-team-dev !erun-excluded
  Port
  Port 17022
`
	entries := ParseSSHHostEntries(content)
	if len(entries) != 1 {
		t.Fatalf("a negated name excludes, it does not declare: %+v", entries)
	}
	if entries[0].Alias != "erun-team-dev" || entries[0].Port != 17022 {
		t.Fatalf("a bare directive must not clobber the block's port: %+v", entries[0])
	}
}

func TestFindOrphanedSSHAliasesNamesWhatEachStaleBlockNowReaches(t *testing.T) {
	// The reported host in miniature: erun-erun-proxmox1 names an environment
	// that does not exist, and 17122 is now live env erun/petios's; erun-erun-
	// local names no live environment either and its port is held by nobody;
	// erun-erun-remote's block is claimed by a configured environment; and
	// myserver is the operator's own, so erun never reports on it.
	entries := []SSHHostEntry{
		{Alias: "erun-erun-proxmox1", Port: 17122},
		{Alias: "erun-erun-local", Port: 17022},
		{Alias: "erun-erun-remote", Port: 17222},
		{Alias: "myserver", Port: 22},
	}
	environments := []SSHEnvironmentAlias{
		{Tenant: "erun", Environment: "petios", Alias: "erun-erun-petios", LocalPort: 17122},
		{Tenant: "erun", Environment: "remote", Alias: "erun-erun-remote", LocalPort: 17222},
	}

	orphans := FindOrphanedSSHAliases(entries, environments)
	if len(orphans) != 2 {
		t.Fatalf("expected the two unclaimed erun blocks and nothing else, got %+v", orphans)
	}
	colliding := orphans[0]
	if colliding.Alias != "erun-erun-proxmox1" || colliding.ReachesTenant != "erun" || colliding.ReachesEnvironment != "petios" {
		t.Fatalf("a block whose port a live environment holds must name it: %+v", colliding)
	}
	harmless := orphans[1]
	if harmless.Alias != "erun-erun-local" || harmless.ReachesEnvironment != "" {
		t.Fatalf("a block nothing holds must stay unqualified: %+v", harmless)
	}
}

func TestFindOrphanedSSHAliasesDoesNotReportABlockAnEnvironmentStillClaims(t *testing.T) {
	entries := []SSHHostEntry{{Alias: "erun-team-dev", Port: 17022}}
	environments := []SSHEnvironmentAlias{
		{Tenant: "team", Environment: "dev", Alias: SSHHostAlias("team", "dev"), LocalPort: 17022},
	}
	if orphans := FindOrphanedSSHAliases(entries, environments); len(orphans) != 0 {
		t.Fatalf("a claimed alias is not stale, got %+v", orphans)
	}
	// And with nothing configured at all, every erun block is stale -- the
	// environment whose block it was is gone.
	if orphans := FindOrphanedSSHAliases(entries, nil); len(orphans) != 1 {
		t.Fatalf("with no environments every erun block is stale, got %+v", orphans)
	}
}
