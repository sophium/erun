package exectool

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimedefaults"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// Preview resolves the supervisor invocation, where startedByJobId must appear.
func TestExecRawBackgroundThreadsTheCallersJobIDToTheSupervisor(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", "erun-test-supervisor-stub")
	runtime := runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: t.TempDir()}})

	for _, tc := range []struct {
		name  string
		input string
		want  bool
	}{
		{"supplied", "gate-parent", true},
		{"omitted", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, envelope, err := RawTool(runtime)(context.Background(), nil, RawInput{
				Command:        []string{"true"},
				Wait:           boolPtr(false),
				Preview:        true,
				StartedByJobID: tc.input,
			})
			if err != nil {
				t.Fatalf("exec_raw: %v", err)
			}
			trace := strings.Join(envelope.Trace, "\n")
			got := strings.Contains(trace, fmt.Sprintf("--started-by-job-id %s", tc.input)) && tc.input != ""
			if got != tc.want {
				t.Fatalf("supervisor invocation carried the parent job id = %v, want %v; trace:\n%s", got, tc.want, trace)
			}
			if tc.input == "" && strings.Contains(trace, "--started-by-job-id") {
				t.Fatalf("supervisor invocation named a parent nobody supplied; trace:\n%s", trace)
			}
		})
	}
}
