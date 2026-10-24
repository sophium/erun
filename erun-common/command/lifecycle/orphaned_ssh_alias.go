package lifecycle

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// SSHEnvironmentAlias is an environment's derived ssh Host alias and local port. Only an
// environment with sshd enabled claims its alias.
type SSHEnvironmentAlias struct {
	Tenant      string
	Environment string
	Alias       string
	// LocalPort is zero when the environment has no ssh-forward.
	LocalPort int
}

// SSHOrphanedAlias is an erun Host block in ~/.ssh/config that no configured environment claims.
type SSHOrphanedAlias struct {
	Alias string `json:"alias"`
	// Port is zero when the block declares none.
	Port int `json:"port,omitempty"`
	// ReachesTenant/ReachesEnvironment name the environment that now owns Port, so ssh to this
	// alias silently lands there. Empty means the block simply fails to resolve.
	ReachesTenant      string `json:"reachesTenant,omitempty"`
	ReachesEnvironment string `json:"reachesEnvironment,omitempty"`
}

// FindOrphanedSSHAliases returns erun-derived Host blocks no configured environment claims;
// operator-written aliases are left alone.
func FindOrphanedSSHAliases(entries []config.SSHHostEntry, environments []SSHEnvironmentAlias) []SSHOrphanedAlias {
	if len(entries) == 0 {
		return nil
	}
	claims := newSSHAliasClaims(environments)
	orphans := make([]SSHOrphanedAlias, 0, 2)
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		alias := strings.TrimSpace(entry.Alias)
		if !claims.isOrphan(alias) || seen[alias] {
			continue
		}
		seen[alias] = true
		orphans = append(orphans, claims.orphan(alias, entry.Port))
	}
	return orphans
}

type sshAliasClaims struct {
	claimed    map[string]bool
	portOwners map[int]SSHEnvironmentAlias
}

func newSSHAliasClaims(environments []SSHEnvironmentAlias) sshAliasClaims {
	claims := sshAliasClaims{
		claimed:    make(map[string]bool, len(environments)),
		portOwners: make(map[int]SSHEnvironmentAlias, len(environments)),
	}
	for _, environment := range environments {
		alias := strings.TrimSpace(environment.Alias)
		if alias == "" {
			continue
		}
		claims.claimed[alias] = true
		if environment.LocalPort <= 0 {
			continue
		}
		// First writer wins so a shared port gets a stable owner.
		if _, taken := claims.portOwners[environment.LocalPort]; !taken {
			claims.portOwners[environment.LocalPort] = environment
		}
	}
	return claims
}

func (c sshAliasClaims) isOrphan(alias string) bool {
	if alias == "" || !strings.HasPrefix(alias, open.SSHHostAliasPrefix) {
		return false
	}
	return !c.claimed[alias]
}

func (c sshAliasClaims) orphan(alias string, port int) SSHOrphanedAlias {
	orphan := SSHOrphanedAlias{Alias: alias, Port: port}
	if owner, ok := c.portOwners[port]; ok {
		orphan.ReachesTenant = owner.Tenant
		orphan.ReachesEnvironment = owner.Environment
	}
	return orphan
}
