package setup

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// displayShapedStateNames are the strings a tenant and an environment take on
// when they are rendered for a human instead of joined into a path: the pair,
// and the pair with the port an operator sees beside it. Directories named this
// way accumulated under the state root, empty, one per pair somebody had seen
// displayed.
var displayShapedStateNames = []string{
	"erun ux",
	"erun build",
	"erun local-ideas",
	"frs local",
	"frs prod 17300",
	"frs local 17700",
	"petios rihards-review",
}

// TestBootstrapDryRunTracesNoWriteItWouldRefuse keeps the dry-run trace honest:
// a bootstrap dry run reports the mkdir and write-yaml the real run would
// perform, so a name the real save refuses must produce neither. Tracing it
// anyway would show the operator a plan that cannot run -- and, for the shape
// this guards, a path with a space in it.
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
