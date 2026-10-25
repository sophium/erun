package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// orchestratorMCPServer runs `erun mcp proxy` over stdio; the proxy mints a bearer
// per request, so no credential is written here.
type orchestratorMCPServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type orchestratorMCPConfig struct {
	MCPServers map[string]orchestratorMCPServer `json:"mcpServers"`
}

type mcpPortResolver func(tenant, environment string) int

type mcpReachabilityProber func(port int) bool

// mcpEnvTypeResolver lets the builder tell an env with no MCP edge at all (host)
// from one whose edge failed to answer.
type mcpEnvTypeResolver func(tenant, environment string) configpkg.EnvironmentType

// orchestratorMCPSkip is a linked env with no MCP entry, reported so the
// orchestrator does not mistake it for unlinked.
type orchestratorMCPSkip struct {
	Label  string
	Reason string
	// HostEnv marks a skip by design, not a failure.
	HostEnv bool
}

// orchestratorMCPOnlyHostSkips: all-host is a working configuration, not an error.
func orchestratorMCPOnlyHostSkips(skipped []orchestratorMCPSkip) bool {
	if len(skipped) == 0 {
		return false
	}
	for _, skip := range skipped {
		if !skip.HostEnv {
			return false
		}
	}
	return true
}

// splitOrchestratorMCPHostSkips separates by-design skips from failures; they need
// different wording and recovery.
func splitOrchestratorMCPHostSkips(skipped []orchestratorMCPSkip) (hostEnvs, problems []orchestratorMCPSkip) {
	for _, skip := range skipped {
		if skip.HostEnv {
			hostEnvs = append(hostEnvs, skip)
			continue
		}
		problems = append(problems, skip)
	}
	return hostEnvs, problems
}

// orchestratorMCPUnreachable is a wired env whose edge did not answer at launch.
// It stays wired because the proxy recovers transient outages per request.
type orchestratorMCPUnreachable struct {
	Label string
}

type orchestratorMCPWiredEnv struct {
	Label string
	Port  int
}

// buildOrchestratorMCPConfig keys servers "<tenant>-<environment>". Skipped envs
// are returned; unanswering edges stay wired but are reported.
func buildOrchestratorMCPConfig(envs []configpkg.OrchestratorEnvConfig, executable string, mcpPort mcpPortResolver, envType mcpEnvTypeResolver, reachable mcpReachabilityProber) (orchestratorMCPConfig, []orchestratorMCPSkip, []orchestratorMCPUnreachable) {
	servers := map[string]orchestratorMCPServer{}
	var skipped []orchestratorMCPSkip
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return orchestratorMCPConfig{MCPServers: servers}, nil, nil
	}
	var wired []orchestratorMCPWiredEnv
	for _, env := range envs {
		tenant := strings.TrimSpace(env.Tenant)
		environment := strings.TrimSpace(env.Environment)
		if tenant == "" || environment == "" {
			skipped = append(skipped, orchestratorMCPSkip{
				Label:  orchestratorEnvLabel(tenant, environment),
				Reason: "the linked entry names no tenant or environment",
			})
			continue
		}
		// Checked before the port lookup: allocation is type-blind, so a host env
		// resolves a port too.
		if envType != nil && envType(tenant, environment) == configpkg.EnvironmentTypeHost {
			skipped = append(skipped, orchestratorMCPSkip{
				Label:   orchestratorEnvLabel(tenant, environment),
				Reason:  "it is a host environment, which has no pod and so no MCP edge to reach",
				HostEnv: true,
			})
			continue
		}
		port := mcpPort(tenant, environment)
		if port <= 0 {
			skipped = append(skipped, orchestratorMCPSkip{
				Label:  orchestratorEnvLabel(tenant, environment),
				Reason: "it resolved no MCP port",
			})
			continue
		}
		servers[tenant+"-"+environment] = orchestratorMCPServer{
			Type:    "stdio",
			Command: executable,
			Args:    []string{"mcp", "proxy", "--tenant", tenant, "--environment", environment},
		}
		wired = append(wired, orchestratorMCPWiredEnv{Label: orchestratorEnvLabel(tenant, environment), Port: port})
	}
	return orchestratorMCPConfig{MCPServers: servers}, skipped, probeOrchestratorMCPEdges(wired, reachable)
}

// probeOrchestratorMCPEdges probes concurrently so launch latency stays at one probe.
func probeOrchestratorMCPEdges(wired []orchestratorMCPWiredEnv, reachable mcpReachabilityProber) []orchestratorMCPUnreachable {
	if len(wired) == 0 || reachable == nil {
		return nil
	}
	answered := make([]bool, len(wired))
	var wg sync.WaitGroup
	for i, env := range wired {
		wg.Add(1)
		go func(i, port int) {
			defer wg.Done()
			answered[i] = reachable(port)
		}(i, env.Port)
	}
	wg.Wait()
	var unreachable []orchestratorMCPUnreachable
	for i, env := range wired {
		if !answered[i] {
			unreachable = append(unreachable, orchestratorMCPUnreachable{Label: env.Label})
		}
	}
	return unreachable
}

func orchestratorEnvLabel(tenant, environment string) string {
	switch {
	case tenant == "" && environment == "":
		return "an unnamed linked entry"
	case tenant == "":
		return "?/" + environment
	case environment == "":
		return tenant + "/?"
	default:
		return tenant + "/" + environment
	}
}

// Distinct because the operator's fix differs.
var (
	errOrchestratorMCPExecutable = errors.New("the erun executable could not be resolved")
	errOrchestratorMCPNoPort     = errors.New("no linked environment resolved an MCP port")
)

func orchestratorMCPUnwiredNotice(name string, err error) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "The orchestrator"
	}
	cause, recovery := err.Error(), "Restart the orchestrator once that is resolved."
	switch {
	case errors.Is(err, errOrchestratorMCPExecutable):
		cause = errOrchestratorMCPExecutable.Error()
		recovery = "Install the erun command line tool, then restart the orchestrator."
	case errors.Is(err, errOrchestratorMCPNoPort):
		cause = errOrchestratorMCPNoPort.Error()
		recovery = "Check its linked environments still exist, then restart the orchestrator."
	}
	return fmt.Sprintf("%s started without its environment tools: %s. %s", label, cause, recovery)
}

// orchestratorMCPUnwiredAction also links install docs for the missing-executable cause.
func orchestratorMCPUnwiredAction(err error) string {
	if errors.Is(err, errOrchestratorMCPExecutable) {
		return notificationActionInstallAndRestartOrchestrator
	}
	return notificationActionRestartOrchestrator
}

// orchestratorMCPPartialNotice reports a partially wired orchestrator. wired and
// total count all linked envs, host ones included, so the sentence stays true.
func orchestratorMCPPartialNotice(name string, wired, total int, hostEnvs, skipped []orchestratorMCPSkip) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "The orchestrator"
	}
	missing := make([]string, 0, len(skipped))
	for _, skip := range skipped {
		missing = append(missing, fmt.Sprintf("%s (%s)", skip.Label, skip.Reason))
	}
	// Absent with no host env, so that message is unchanged.
	hostNote := ""
	if len(hostEnvs) > 0 {
		names := make([]string, 0, len(hostEnvs))
		for _, env := range hostEnvs {
			names = append(names, env.Label)
		}
		hostNote = fmt.Sprintf(" (no MCP edge is expected for %s)", strings.Join(names, ", "))
	}
	return fmt.Sprintf("%s started with tools for %d of %d linked environments%s. Missing: %s. Check those environments still exist, then restart the orchestrator.",
		label, wired, total, hostNote, strings.Join(missing, "; "))
}

// orchestratorMCPHostEnvNotice is informational: host envs have no edge by design.
func orchestratorMCPHostEnvNotice(name string, hostEnvs []orchestratorMCPSkip) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "The orchestrator"
	}
	names := make([]string, 0, len(hostEnvs))
	for _, env := range hostEnvs {
		names = append(names, env.Label)
	}
	return fmt.Sprintf("%s links %s without MCP tools: a host environment has no pod, so it has no MCP edge. "+
		"Work in its directory directly.", label, strings.Join(names, ", "))
}

// orchestratorMCPUnreachableNotice names envs that are wired but whose edge was
// down at launch; they recover on their own.
func orchestratorMCPUnreachableNotice(name string, unreachable []orchestratorMCPUnreachable) string {
	if len(unreachable) == 0 {
		return ""
	}
	label := strings.TrimSpace(name)
	if label == "" {
		label = "The orchestrator"
	}
	names := make([]string, 0, len(unreachable))
	for _, env := range unreachable {
		names = append(names, env.Label)
	}
	return fmt.Sprintf("%s wired tools for %s, but its edge is not answering right now. "+
		"Calls will recover automatically once the edge comes back; if it stays down, deploy or reopen that environment.",
		label, strings.Join(names, ", "))
}

// orchestratorMCPUnreachableEnv reports false for malformed labels ("?" halves), so
// no deploy action is offered.
func orchestratorMCPUnreachableEnv(label string) (tenant, environment string, ok bool) {
	tenant, environment, found := strings.Cut(label, "/")
	if !found || tenant == "" || environment == "" || tenant == "?" || environment == "?" {
		return "", "", false
	}
	return tenant, environment, true
}

// orchestratorEdgeRepairBudget matches the MCP client's own first-connect window
// (mcpStartupReachabilityWait); it is a ceiling, not a delay.
const orchestratorEdgeRepairBudget = 20 * time.Second

// repairOrchestratorMCPEdges must finish before the config is written: the client
// connects only once, at launch. It is not a gate; the next probe reports failures.
func (a *App) repairOrchestratorMCPEdges(envs []configpkg.OrchestratorEnvConfig) {
	dead := a.orchestratorEdgesNotAnswering(envs)
	if len(dead) == 0 {
		return
	}
	// Concurrent, so several dead links share one budget.
	var wg sync.WaitGroup
	for _, selection := range dead {
		wg.Add(1)
		go func(selection uimodel.Selection) {
			defer wg.Done()
			a.ensureEnvRuntimeWithin(selection, envEnsureForBrokenForward, orchestratorEdgeRepairBudget)
		}(selection)
	}
	wg.Wait()
}

// A missing probe means "cannot tell", never "not answering": do not restart a
// forward on no evidence.
func (a *App) orchestratorEdgesNotAnswering(envs []configpkg.OrchestratorEnvConfig) []uimodel.Selection {
	var dead []uimodel.Selection
	for _, ref := range envs {
		tenant := strings.TrimSpace(ref.Tenant)
		environment := strings.TrimSpace(ref.Environment)
		if tenant == "" || environment == "" || a.orchestratorRefNamesHostEnv(ref) {
			continue
		}
		port := a.orchestratorMCPPort(tenant, environment)
		if port <= 0 || a.mcpEdgeAnswers(port) {
			continue
		}
		dead = append(dead, uimodel.Selection{Tenant: tenant, Environment: environment})
	}
	return dead
}

// Shared with the builder so the forward opened is the one about to be wired.
func (a *App) orchestratorMCPPort(tenant, environment string) int {
	ports, err := configpkg.ResolveEnvironmentLocalPorts(a.deps.store, tenant, environment)
	if err != nil {
		return 0
	}
	return ports.MCP
}

// An unloadable env has no type, so it falls through to the port path and is
// reported as a failure, never excused as host.
func (a *App) orchestratorMCPEnvType(tenant, environment string) configpkg.EnvironmentType {
	env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return ""
	}
	return env.ResolvedType()
}

// writeOrchestratorMCPConfig writes the per-orchestrator --mcp-config file. It
// returns "" with an error when nothing was wired, except when every env is host.
func (a *App) writeOrchestratorMCPConfig(id string, envs []configpkg.OrchestratorEnvConfig) (string, []orchestratorMCPSkip, []orchestratorMCPUnreachable, error) {
	if len(envs) == 0 {
		return "", nil, nil, nil
	}
	// Without an erun binary there is no proxy, so launch without envs.
	executable, err := jobs.ResolveErunExecutable()
	if err != nil {
		return "", nil, nil, fmt.Errorf("%w: %w", errOrchestratorMCPExecutable, err)
	}
	config, skipped, unreachable := buildOrchestratorMCPConfig(envs, executable,
		a.orchestratorMCPPort, a.orchestratorMCPEnvType, a.deps.canReachMCPEndpoint)
	if len(config.MCPServers) == 0 {
		if orchestratorMCPOnlyHostSkips(skipped) {
			return "", skipped, nil, nil
		}
		return "", skipped, nil, errOrchestratorMCPNoPort
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", skipped, unreachable, err
	}
	path := orchestratorMCPConfigPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", skipped, unreachable, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", skipped, unreachable, err
	}
	return path, skipped, unreachable, nil
}

// A shared workspace cannot hold a per-orchestrator .mcp.json, so it lives under UserConfigDir.
func orchestratorMCPConfigPath(id string) string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", "orchestrator-mcp-"+sanitizeOrchestratorFileID(id)+".json")
}

func sanitizeOrchestratorFileID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "default"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, id)
}
