package eruncommon

import "strings"

// SSHEnvironmentAlias is one configured environment's standing on the host's
// ssh config: the Host alias erun derives for it, and the local port its
// sshd is reachable on. Only an environment whose sshd is enabled is offered
// as a claimant -- with sshd off nothing on this host answers for that alias,
// and a block still naming it is exactly the stale one this report is about.
type SSHEnvironmentAlias struct {
	Tenant      string
	Environment string
	Alias       string
	// LocalPort is the port the environment's ssh-forward binds, zero when it
	// has none.
	LocalPort int
}

// SSHOrphanedAlias is a Host block in ~/.ssh/config that names an erun
// environment alias no configured environment claims.
type SSHOrphanedAlias struct {
	Alias string `json:"alias"`
	// Port is the local port the block forwards to, zero when it declares none.
	Port int `json:"port,omitempty"`
	// ReachesTenant / ReachesEnvironment name the environment whose local ssh
	// port now belongs to this block's Port, when one does. This is the serious
	// half: the alias names an environment that does not exist and reaches a
	// different one that does, so ssh, scp, VS Code Remote-SSH and workspace
	// sync all connect, authenticate and operate somewhere the operator did not
	// name. Empty means the port is held by no environment, so the block only
	// fails to resolve, which is legible.
	ReachesTenant      string `json:"reachesTenant,omitempty"`
	ReachesEnvironment string `json:"reachesEnvironment,omitempty"`
}

// FindOrphanedSSHAliases returns the erun-derived Host blocks in entries that
// no configured environment claims. A block for any other alias is left alone:
// erun reports on the aliases it writes, never on an operator's own.
//
// Claim is by alias name, and an environment claims its alias only while its
// sshd is enabled -- see SSHEnvironmentAlias. A port an environment has
// allocated but does not serve is not a destination, so it does not make a
// stale block serious.
func FindOrphanedSSHAliases(entries []SSHHostEntry, environments []SSHEnvironmentAlias) []SSHOrphanedAlias {
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

// sshAliasClaims is what the configured environments answer about one alias:
// whether any of them answers for it, and which one a given local port now
// belongs to.
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
		// First writer wins, so a host with two environments persisted on one
		// port gets a stable answer rather than map iteration order.
		if _, taken := claims.portOwners[environment.LocalPort]; !taken {
			claims.portOwners[environment.LocalPort] = environment
		}
	}
	return claims
}

// isOrphan reports whether alias is one erun derives and no environment
// answers for. An alias erun did not derive belongs to the operator.
func (c sshAliasClaims) isOrphan(alias string) bool {
	if alias == "" || !strings.HasPrefix(alias, SSHHostAliasPrefix) {
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
