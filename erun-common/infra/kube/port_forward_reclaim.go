package kube

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// Records whose environment the config store no longer knows are never opened or rotated
// again, so the sweep removes them. It runs only from forward setup, beside log rotation.

// portForwardRoot resolves through the same config root as PortForwardStatePath.
func portForwardRoot() (string, error) {
	return config.ResolveConfigFilePath(filepath.Join(config.ConfigRoot, "portforward"))
}

// ReclaimOrphanedPortForwardRecords removes forward records for environments the config store
// no longer knows. An unreadable config leaves the tree untouched.
func ReclaimOrphanedPortForwardRecords(ctx core.Context) error {
	configured, known, err := configuredEnvironmentNames()
	if err != nil {
		return err
	}
	if !known {
		ctx.Trace("portforward: skipping the orphan reclaim: the config store could not be listed, so which environments exist is unknown")
		return nil
	}
	root, err := portForwardRoot()
	if err != nil {
		return err
	}
	// Also sweep the legacy os.UserConfigDir tree, which migration never reaches for deleted environments.
	roots := []string{root}
	if legacyRoot, ok := legacyPortForwardRoot(); ok {
		roots = append(roots, legacyRoot)
	}
	reclaimed := map[string]bool{}
	for _, root := range roots {
		for _, kind := range PortForwardStateKinds {
			kindDir := filepath.Join(root, kind)
			if err := reclaimOrphanedRecordsInKind(ctx, kindDir, kind, configured, reclaimed); err != nil {
				return err
			}
		}
	}
	return nil
}

func readDirIfPresent(dir string) ([]os.DirEntry, bool) {
	entries, err := os.ReadDir(dir)
	return entries, err == nil
}

func reclaimOrphanedRecordsInKind(ctx core.Context, kindDir, kind string, configured map[string]map[string]bool, reclaimed map[string]bool) error {
	tenantEntries, ok := readDirIfPresent(kindDir)
	if !ok {
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
			key := filepath.Join(kind, tenant, environment)
			if reclaimed[key] {
				continue
			}
			reclaimed[key] = true
			if err := RemovePortForwardRecord(ctx, kind, tenant, environment); err != nil {
				return err
			}
		}
		removeDirIfEmpty(ctx, tenantDir)
	}
	removeDirIfEmpty(ctx, kindDir)
	return nil
}

// portForwardRecordNames reads file names, not records: the residue is often a log
// whose state file is already gone.
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

// configuredEnvironmentNames returns the configured tenant -> environment names. known=false
// means the store is uninitialized or unreadable, and the caller must not treat that as
// "nothing configured", which would remove every forward log.
func configuredEnvironmentNames() (map[string]map[string]bool, bool, error) {
	store := config.ConfigStore{}
	if !machineConfigLoads(store) {
		return nil, false, nil
	}
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
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

// removeDirIfEmpty relies on os.Remove refusing a populated directory.
func removeDirIfEmpty(ctx core.Context, dir string) {
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

func machineConfigLoads(store config.ConfigStore) bool {
	_, _, err := store.LoadERunConfig()
	return err == nil
}
