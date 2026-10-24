package eruncommon

import (
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/orchestration"
)

func TestAnonymousPullabilityBaselineIsCurrent(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	names, err := orchestration.DiscoverModuleReferencedImageNames(filepath.Join(root, "erun-devops", "terraform-erun"))
	if err != nil {
		t.Fatalf("scan terraform modules: %v", err)
	}
	referenced := make(map[string]bool, len(names))
	for _, name := range names {
		referenced[name] = true
	}
	for name := range orchestration.AnonymousPullabilityBaseline {
		if !referenced[name] {
			t.Errorf("anonymousPullabilityBaseline names %q, but no terraform module references it any more -- remove the stale entry", name)
		}
	}
}
