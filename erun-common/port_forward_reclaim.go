package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The forward tree outlives the environments it describes. A state file is
// reclaimed when its environment is deleted, but the log beside it was not,
// and a record naming a tenant or environment the config store does not know
// is one nothing will ever open, restart, or rotate again: the per-log cap
// bounds a log that is still being written, and says nothing about the ones
// nobody will write to again. Those are the files that keep the tree growing
// across reports of it, including whole tenant directories whose environments
// were deleted months earlier.
//
// Reclaiming on deletion alone would only cover environments deleted after it
// landed; the residue this exists for is already on disk. So the sweep
// compares the tree against the config store — the only authority on which
// environments exist — and removes the records the store does not know.
//
// It runs where the forward store is already touched, beside the rotation that
// bounds a live log (`erun open`'s forward setup). Never on a timer, and never
// from a command that has nothing to do with forwards: a bound enforced only
// by something unrelated to what it bounds is a bound that quietly stops being
// enforced.

// PortForwardRoot is the directory holding every forward record, one subtree
// per kind. It is the parent of PortForwardStatePath's own directory, resolved
// through the same config root so a sweep and a write cannot disagree about
// where the tree is.
func PortForwardRoot() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, configRoot, "portforward"), nil
}

// ReclaimOrphanedPortForwardRecords removes every forward record under the
// forward tree whose environment the config store no longer knows. It is
// best-effort by construction: an environment whose configured set cannot be
// read is left entirely alone, so "the config could not be listed" can never
// be mistaken for "no environment exists" and take a live forward's log with
// it.
func ReclaimOrphanedPortForwardRecords(ctx Context) error {
	configured, known, err := configuredEnvironmentNames()
	if err != nil {
		return err
	}
	if !known {
		ctx.Trace("portforward: skipping the orphan reclaim: the config store could not be listed, so which environments exist is unknown")
		return nil
	}
	root, err := PortForwardRoot()
	if err != nil {
		return err
	}
	for _, kind := range portForwardStateKinds {
		kindDir := filepath.Join(root, kind)
		if err := reclaimOrphanedRecordsInKind(ctx, kindDir, kind, configured); err != nil {
			return err
		}
	}
	return nil
}

func reclaimOrphanedRecordsInKind(ctx Context, kindDir, kind string, configured map[string]map[string]bool) error {
	tenantEntries, err := os.ReadDir(kindDir)
	if err != nil {
		// Nothing recorded for this kind yet, which is the ordinary state of a
		// machine that never opened a forward of it.
		return nil
	}
	for _, tenantEntry := range tenantEntries {
		if !tenantEntry.IsDir() {
			continue
		}
		tenant := tenantEntry.Name()
		tenantDir := filepath.Join(kindDir, tenant)
		environments, tenantKnown := configured[tenant]
		names, err := portForwardRecordNames(tenantDir)
		if err != nil {
			return err
		}
		for _, environment := range names {
			if tenantKnown && environments[environment] {
				continue
			}
			if err := RemovePortForwardRecord(ctx, kind, tenant, environment); err != nil {
				return err
			}
		}
		removeDirIfEmpty(ctx, tenantDir)
	}
	removeDirIfEmpty(ctx, kindDir)
	return nil
}

// portForwardRecordNames lists the environments a tenant's directory holds
// records for, taken from the file names rather than from the records
// themselves: the files that most need reclaiming are the ones whose state
// file is already gone and only a log is left, so enumerating state files
// alone would miss exactly the residue this sweep exists for.
func portForwardRecordNames(tenantDir string) ([]string, error) {
	entries, err := os.ReadDir(tenantDir)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(entries))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".log.1"):
			name = strings.TrimSuffix(name, ".log.1")
		case strings.HasSuffix(name, ".log"):
			name = strings.TrimSuffix(name, ".log")
		case strings.HasSuffix(name, ".json"):
			name = strings.TrimSuffix(name, ".json")
		default:
			continue
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// configuredEnvironmentNames is the set of tenant -> environment names the
// config store knows. known=false means the store could not be read well
// enough to answer at all, and the caller must leave the tree as it is: "I
// could not read the config" must never read as "no environment is
// configured", because that answer removes every forward log on the host.
//
// What separates the two is whether the store was ever initialized. A store
// that reads cleanly and lists no tenants is a real answer — nothing is
// configured, so nothing under the tree is a live environment's record — but a
// store that was never initialized here is not that answer: it is the shape of
// two halves of erun resolving different config homes, where the tree in front
// of the sweep belongs to a root this process cannot see. Listing a missing
// directory yields no tenants without reporting anything, so the two cases are
// only distinguishable by asking the question the store answers differently —
// whether the machine-level config it is initialized around is there.
func configuredEnvironmentNames() (map[string]map[string]bool, bool, error) {
	store := ConfigStore{}
	if _, _, err := store.LoadERunConfig(); err != nil {
		return nil, false, nil
	}
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return nil, false, nil
		}
		return nil, false, err
	}
	configured := make(map[string]map[string]bool, len(tenants))
	for _, tenant := range tenants {
		name := strings.TrimSpace(tenant.Name)
		if name == "" {
			continue
		}
		envs, err := store.ListEnvConfigs(name)
		if err != nil {
			return nil, false, err
		}
		environments := make(map[string]bool, len(envs))
		for _, env := range envs {
			if environment := strings.TrimSpace(env.Name); environment != "" {
				environments[environment] = true
			}
		}
		configured[name] = environments
	}
	return configured, true, nil
}

// removeDirIfEmpty removes a directory the sweep has emptied. Only an empty
// one: os.Remove refuses a populated directory, and the tenant directory left
// behind by a tenant deleted long ago is the same residue in a smaller shape.
func removeDirIfEmpty(ctx Context, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		return
	}
	ctx.TraceCommand("", "rmdir", dir)
	if ctx.DryRun {
		return
	}
	_ = os.Remove(dir)
}
