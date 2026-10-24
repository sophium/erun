package setup

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// displayShapedStateNames are tenant/env pairs as rendered for humans, which
// once leaked into the state root as directory names.
var displayShapedStateNames = []string{
	"erun ux",
	"erun build",
	"erun local-ideas",
	"frs local",
	"frs prod 17300",
	"frs local 17700",
	"petios rihards-review",
}

// A name the real save refuses must not appear in the dry-run trace.
func TestBootstrapDryRunTracesNoWriteItWouldRefuse(t *testing.T) {
	redirectConfigHomeForTest(t)

	for _, name := range displayShapedStateNames {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			store := tracedBootstrapStore{
				ctx: core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityTrace, &stdout, &stderr), DryRun: true},
			}
			if err := store.SaveEnvConfig("frs", config.EnvConfig{Name: name}); !errors.Is(err, config.ErrUnusableStateName) {
				t.Fatalf("SaveEnvConfig environment %q = %v, want ErrUnusableStateName", name, err)
			}
			if err := store.SaveTenantConfig(config.TenantConfig{Name: name}); !errors.Is(err, config.ErrUnusableStateName) {
				t.Fatalf("SaveTenantConfig %q = %v, want ErrUnusableStateName", name, err)
			}
			traced := stdout.String() + stderr.String()
			if strings.Contains(traced, "mkdir") || strings.Contains(traced, "write-yaml") {
				t.Fatalf("a dry run traced a write the real run would refuse:\n%s", traced)
			}
		})
	}
}
