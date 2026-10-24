package registrar

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-mcp/tools/shared/callerid"
)

func TestOperateCapabilityReachesItsHandlerAtCallTime(t *testing.T) {
	identity := callerid.AuthIdentity{
		Tenant:       "acme",
		Capabilities: mcptools.NewMCPCapabilitySet([]string{string(mcptools.MCPCapabilityOperate)}),
	}
	called := false
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		called = true
		return nil, struct{}{}, nil
	}

	guarded := GuardTool(identity, "deploy", nil, handler)
	if _, _, err := guarded(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("an operate capability must reach deploy's handler: %v", err)
	}
	if !called {
		t.Fatal("deploy's handler must run for an operate-scoped caller")
	}
}

// Defence in depth for handlers reached without registration.
func TestGuardRefusesEvenWhenAHandlerIsReachedDirectly(t *testing.T) {
	identity := callerid.AuthIdentity{
		Tenant:       "acme",
		Capabilities: mcptools.NewMCPCapabilitySet([]string{string(mcptools.MCPCapabilityRead)}),
	}
	called := false
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		called = true
		return nil, struct{}{}, nil
	}

	guarded := GuardTool(identity, "raw", nil, handler)
	if _, _, err := guarded(context.Background(), nil, struct{}{}); err == nil {
		t.Fatal("the guard must refuse a tool outside the caller's capabilities")
	}
	if called {
		t.Fatal("a refused tool must not run its handler")
	}
}

// captureAuditLog swaps the process-global standard logger and restores it on cleanup.
func captureAuditLog(t *testing.T) *strings.Builder {
	t.Helper()
	var out strings.Builder
	prev := log.Writer()
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &out
}

// guardTool's closure is shared across calls, so identity must not be shared state.
func TestGuardToolAttributesEachConcurrentCallToItsOwnCaller(t *testing.T) {
	out := captureAuditLog(t)

	capabilities := mcptools.NewMCPCapabilitySet([]string{string(mcptools.MCPCapabilityRead)})
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		return nil, struct{}{}, nil
	}
	guarded := GuardTool(callerid.AuthIdentity{Tenant: "acme", User: "registration-time", Capabilities: capabilities}, "list", nil, handler)

	const callers = 200
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			ctx := callerid.WithAuthIdentity(context.Background(), callerid.AuthIdentity{
				Tenant:       "acme",
				User:         fmt.Sprintf("user-%d", i),
				Capabilities: capabilities,
			})
			<-start
			if _, _, err := guarded(ctx, nil, struct{}{}); err != nil {
				t.Errorf("call %d: unexpected refusal: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[string]int, callers)
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		idx := strings.Index(line, "user=")
		if idx < 0 {
			t.Fatalf("audit line has no user field: %q", line)
		}
		field := line[idx+len("user="):]
		if sp := strings.IndexByte(field, ' '); sp >= 0 {
			field = field[:sp]
		}
		seen[field]++
	}
	for i := 0; i < callers; i++ {
		want := fmt.Sprintf("user-%d", i)
		if seen[want] != 1 {
			t.Fatalf("caller %q was audited %d times, want exactly 1 (audit log: %s)", want, seen[want], out.String())
		}
	}
}

func TestGuardToolWithNoAuthContextDoesNotInheritThePreviousCaller(t *testing.T) {
	out := captureAuditLog(t)

	capabilities := mcptools.NewMCPCapabilitySet([]string{string(mcptools.MCPCapabilityRead)})
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		return nil, struct{}{}, nil
	}
	guarded := GuardTool(callerid.AuthIdentity{Tenant: "acme", User: "registration-time", Capabilities: capabilities}, "list", nil, handler)

	liveCtx := callerid.WithAuthIdentity(context.Background(), callerid.AuthIdentity{Tenant: "acme", User: "live-caller", Capabilities: capabilities})
	if _, _, err := guarded(liveCtx, nil, struct{}{}); err != nil {
		t.Fatalf("live caller: unexpected refusal: %v", err)
	}

	if _, _, err := guarded(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("no-auth-context caller: unexpected refusal: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "user=registration-time") {
		t.Fatalf("a call with no auth context must be audited as the registered identity, not the previous live caller: %q", lines[1])
	}
}
