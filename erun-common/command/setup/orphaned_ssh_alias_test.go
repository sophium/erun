package setup

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/primitive/open"
)

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
	want := []config.SSHHostEntry{
		{Alias: "erun-team-dev", HostKeyAlias: "erun-team-dev", HostName: "127.0.0.1", Port: 17022, User: "erun", IdentityFile: "/home/op/.ssh/id_ed25519"},
		{Alias: "erun-team-gone", Port: 17122},
		{Alias: "erun-team-also-gone", Port: 17122},
	}
	got := config.ParseSSHHostEntries(content)
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
	entries := config.ParseSSHHostEntries(content)
	if len(entries) != 1 {
		t.Fatalf("a negated name excludes, it does not declare: %+v", entries)
	}
	if entries[0].Alias != "erun-team-dev" || entries[0].Port != 17022 {
		t.Fatalf("a bare directive must not clobber the block's port: %+v", entries[0])
	}
}

func TestFindOrphanedSSHAliasesDoesNotReportABlockAnEnvironmentStillClaims(t *testing.T) {
	entries := []config.SSHHostEntry{{Alias: "erun-team-dev", Port: 17022}}
	environments := []lifecycle.SSHEnvironmentAlias{
		{Tenant: "team", Environment: "dev", Alias: open.SSHHostAlias("team", "dev"), LocalPort: 17022},
	}
	if orphans := lifecycle.FindOrphanedSSHAliases(entries, environments); len(orphans) != 0 {
		t.Fatalf("a claimed alias is not stale, got %+v", orphans)
	}
	// And with nothing configured at all, every erun block is stale -- the
	// environment whose block it was is gone.
	if orphans := lifecycle.FindOrphanedSSHAliases(entries, nil); len(orphans) != 1 {
		t.Fatalf("with no environments every erun block is stale, got %+v", orphans)
	}
}
