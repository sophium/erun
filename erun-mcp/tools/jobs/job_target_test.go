package jobs

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// Restating the server's own scope, or omitting it, must keep working -- the
// arguments are how a caller disambiguates, and refusing those would break every
// existing caller.
func TestJobToolsAcceptTheirOwnScope(t *testing.T) {
	runtime := shared.RuntimeConfig{Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	ctx := context.Background()

	if _, _, err := JobStatusTool(runtime)(ctx, nil, JobStatusInput{Tenant: "tenant-a", Environment: "dev"}); err != nil {
		t.Errorf("job_status refused its own scope: %v", err)
	}
	if _, _, err := JobStatusTool(runtime)(ctx, nil, JobStatusInput{}); err != nil {
		t.Errorf("job_status refused an omitted scope: %v", err)
	}
}
