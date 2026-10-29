package jobs

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func TestJobToolsAcceptTheirOwnScope(t *testing.T) {
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	ctx := context.Background()

	if _, _, err := JobStatusTool(runtime)(ctx, nil, JobStatusInput{Tenant: "tenant-a", Environment: "dev"}); err != nil {
		t.Errorf("job_status refused its own scope: %v", err)
	}
	if _, _, err := JobStatusTool(runtime)(ctx, nil, JobStatusInput{}); err != nil {
		t.Errorf("job_status refused an omitted scope: %v", err)
	}
}
