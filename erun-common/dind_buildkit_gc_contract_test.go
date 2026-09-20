package eruncommon

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The erun-dind image bakes erun-devops/docker/erun-dind/daemon.json into
// /etc/docker/daemon.json, and that file is the only BuildKit cache size bound
// this repository configures anywhere: runDiskHeadroomPrune in
// release_disk_headroom.go is floor-triggered (--min-free-space) and carries no
// ceiling, so it can only react once free space is already gone.
//
// Being the only bound makes it worth checking mechanically, because a daemon
// config fails in a way nothing else here can see: docker silently ignores a key
// it does not define, so a mislaid bound reads as configured while enforcing
// nothing. That has already happened twice in this file — once as a deprecated
// `defaultKeepStorage` that is a ReservedSpace floor rather than the ceiling it
// was read as, and once as gc-level `reservedSpace`/`maxUsedSpace`/
// `minFreeSpace` keys, which BuilderGCConfig does not define at that level (they
// belong inside a policy entry). Neither produced a build error; both left the
// cache unbounded at 76-90GB per environment.
//
// `dockerd --validate` does not catch it either: it reports "configuration OK"
// for a gc block containing a key that does not exist at all. So this test
// decodes against the exact field sets of the docker version the image pins
// (moby daemon/config.BuilderGCConfig and BuilderGCRule) and rejects anything
// outside them.
//
// This does not and cannot establish that the declared bound *enforces*; only
// watching a running daemon's cache can. It establishes that the file is
// declaring one, in a place docker reads.

// dindDaemonConfigPath is where the sidecar image's daemon config lives.
func dindDaemonConfigPath(root string) string {
	return filepath.Join(root, "erun-devops", "docker", "erun-dind", "daemon.json")
}

// dindGCConfig mirrors moby's daemon/config.BuilderGCConfig. The deprecated
// DefaultKeepStorage field is kept because BuilderGCConfig still accepts it and
// maps it onto DefaultReservedSpace, so rejecting it here would reject a key
// docker really reads.
type dindGCConfig struct {
	Enabled              bool         `json:"enabled"`
	Policy               []dindGCRule `json:"policy"`
	DefaultReservedSpace string       `json:"defaultReservedSpace"`
	DefaultMaxUsedSpace  string       `json:"defaultMaxUsedSpace"`
	DefaultMinFreeSpace  string       `json:"defaultMinFreeSpace"`
	DefaultKeepStorage   string       `json:"defaultKeepStorage"`
}

// dindGCRule mirrors moby's daemon/config.BuilderGCRule. Filter is left raw
// rather than typed: moby accepts both an array of "key=value" strings and a
// deprecated object form, and the shape of a filter is not what this test is
// guarding — a filter that does not match what the author meant is visible in
// the file, whereas an ignored *key* is not.
type dindGCRule struct {
	All           bool            `json:"all"`
	Filter        json.RawMessage `json:"filter"`
	ReservedSpace string          `json:"reservedSpace"`
	MaxUsedSpace  string          `json:"maxUsedSpace"`
	MinFreeSpace  string          `json:"minFreeSpace"`
	KeepStorage   string          `json:"keepStorage"`
}

// decodeDindGCRule decodes one gc.policy entry, rejecting keys docker does not
// read. Split out so the rejection itself can be exercised against a synthetic
// input rather than only against whatever the shipped file happens to contain.
func decodeDindGCRule(raw json.RawMessage) (dindGCRule, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rule dindGCRule
	err := dec.Decode(&rule)
	return rule, err
}

// readDindGCConfig reads and decodes the shipped config's builder.gc block.
func readDindGCConfig(t *testing.T, root string) dindGCConfig {
	t.Helper()
	path := dindDaemonConfigPath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var top struct {
		Builder struct {
			GC json.RawMessage `json:"gc"`
		} `json:"builder"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(top.Builder.GC) == 0 {
		t.Fatalf("%s declares no builder.gc block, so nothing bounds the dind build cache", path)
	}

	dec := json.NewDecoder(bytes.NewReader(top.Builder.GC))
	dec.DisallowUnknownFields()
	var gc dindGCConfig
	if err := dec.Decode(&gc); err != nil {
		t.Fatalf("%s: %v\n\nA gc key docker does not define is silently ignored, so the bound it was meant to "+
			"declare would read as configured while enforcing nothing. Move size limits into a gc.policy entry "+
			"(reservedSpace / maxUsedSpace / minFreeSpace); the gc-level names for them are defaultReservedSpace, "+
			"defaultMaxUsedSpace and defaultMinFreeSpace.", path, err)
	}
	return gc
}

// TestDindBuildKitGCConfigRejectsKeysDockerWouldIgnore proves the check above
// has teeth by feeding it the shape dockerd --validate accepts and this must
// not: a key that does not exist anywhere in docker's schema.
func TestDindBuildKitGCConfigRejectsKeysDockerWouldIgnore(t *testing.T) {
	_, err := decodeDindGCRule(json.RawMessage(`{"maxUsedSpace":"40GB","totallyMadeUpKey":"99GB"}`))
	if err == nil {
		t.Fatal("a gc.policy entry carrying a key docker does not define decoded cleanly, so this test would " +
			"not catch a bound written under a name nothing reads")
	}
}

// TestDindBuildKitGCPolicyDeclaresASizeBound checks the config declares what it
// is the only source of: a cache size ceiling, in a form docker's own config
// loader turns into a BuildKit keepBytes.
func TestDindBuildKitGCPolicyDeclaresASizeBound(t *testing.T) {
	gc := readDindGCConfig(t, repoRootForDockerignoreTest(t))

	if !gc.Enabled {
		t.Error("builder.gc.enabled is false, so BuildKit runs no GC policy at all and the dind cache grows unbounded")
	}
	if len(gc.Policy) == 0 {
		t.Fatal("builder.gc declares no policy entry, so no cache size bound reaches BuildKit")
	}

	// When policy is non-empty moby builds the whole policy from it and never
	// reads the Default* fields, so a bound written there is dead — the same
	// silently-ignored shape, one level up.
	if gc.DefaultMaxUsedSpace != "" || gc.DefaultReservedSpace != "" ||
		gc.DefaultMinFreeSpace != "" || gc.DefaultKeepStorage != "" {
		t.Error("builder.gc sets Default* size fields alongside a non-empty policy, but moby builds the policy " +
			"from policy[] alone when it is present and never reads them — move the values into the policy entry")
	}

	bounded := false
	for _, rule := range gc.Policy {
		if rule.MaxUsedSpace != "" {
			bounded = true
		}
	}
	if !bounded {
		t.Error("no builder.gc.policy entry sets maxUsedSpace, so nothing bounds the build cache's size — only its age")
	}
}
