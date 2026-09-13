package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	eruncommon "github.com/sophium/erun/erun-common"
)

// orchestratorMCPServer is one Claude Code MCP server entry: a linked env's erun
// MCP edge (emcp in the pod, exposing raw/diff/build/push/deploy/outputs_*),
// reached by launching `erun mcp proxy` over stdio. The proxy mints a bearer per
// request, so no credential is written here and a session cannot lose its envs
// partway through to an aged-out token.
type orchestratorMCPServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type orchestratorMCPConfig struct {
	MCPServers map[string]orchestratorMCPServer `json:"mcpServers"`
}

// mcpPortResolver is a seam so the config assembly is unit testable without a
// live config store.
type mcpPortResolver func(tenant, environment string) int

// mcpReachabilityProber reports whether a local MCP edge answers right now.
// The same shape as App.deps.canReachMCPEndpoint, so writeOrchestratorMCPConfig
// passes that seam straight through and this stays unit testable without a
// live port-forward.
type mcpReachabilityProber func(port int) bool

// mcpEnvTypeResolver reports an environment's resolved type (an
// eruncommon.EnvironmentType constant, or its zero value when the environment
// no longer resolves), so the builder can tell an environment that has no MCP
// edge AT ALL from one whose edge merely failed to answer. A host environment
// is the former: it has no pod, so nothing runs the erun MCP edge for it and no
// port-forward can ever reach one. A seam rather than a field on
// eruncommon.OrchestratorEnvConfig, which records where an orchestrator reviews
// an environment, not what the environment is.
type mcpEnvTypeResolver func(tenant, environment string) eruncommon.EnvironmentType

// orchestratorMCPSkip is one linked environment that produced no MCP server
// entry, and why. Carried out of the builder rather than dropped: an
// orchestrator is told by its own operating contract to know which environments
// are its own, so an environment silently missing from its toolset is a
// falsehood it cannot detect from the inside (#1185).
type orchestratorMCPSkip struct {
	Label  string
	Reason string
	// HostEnv marks a skip that is not a defect: a host environment has no pod,
	// so it has no MCP edge by design and the orchestrator drives it through its
	// review directory instead. Kept apart from the other skip reasons so an
	// orchestrator whose linked environments are all host environments — a
	// working configuration — is not reported as the failure every other skip
	// reason describes.
	HostEnv bool
}

// orchestratorMCPOnlyHostSkips reports that the only reason nothing wired is
// that every linked environment is a host environment. That is a working
// configuration rather than a failure, so it must not take the "no linked
// environment resolved an MCP port" error path meant for a real resolution
// failure.
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

// splitOrchestratorMCPHostSkips separates the linked environments that have no
// MCP edge by design from those that failed to wire. The two need different
// words and different recoveries: a host environment exists and works, and
// restarting the orchestrator would change nothing, while every other skip is a
// real problem the partial notice's "check those environments still exist"
// advice does fit.
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

// orchestratorMCPUnreachable is one linked environment that got a wired MCP
// server entry — it is NOT skipped — even though its edge did not answer a
// quick probe at launch time. Reported separately from orchestratorMCPSkip
// because the environment IS wired: erun-common/mcp_proxy.go already recovers
// a transient edge outage per request ("a transient edge outage must not kill
// the session"), so dropping the environment here would strand it for the
// whole session even after the edge recovers. What is missing without this is
// only that an orchestrator whose env has no tools right now cannot tell "not
// linked" apart from "linked but the edge was down at launch".
type orchestratorMCPUnreachable struct {
	Label string
}

// orchestratorMCPWiredEnv is one environment buildOrchestratorMCPConfig wired,
// carried alongside its resolved port so the reachability probe below can
// check it after the server map is assembled.
type orchestratorMCPWiredEnv struct {
	Label string
	Port  int
}

// buildOrchestratorMCPConfig assembles the per-env MCP server map, keyed
// "<tenant>-<environment>". An env is skipped (not an error) when its MCP port
// does not resolve — that env is not wired for MCP at all — so an orchestrator
// still gets the envs it can reach. Every skip is returned alongside, so a
// PARTIAL skip is reportable: previously only the all-envs-failed case produced
// any signal, and a session missing one of two environments looked identical to
// a healthy one until its first call into the missing env.
//
// An env whose port resolves but whose edge does not answer a quick probe is
// wired anyway and reported as unreachable rather than skipped — see
// orchestratorMCPUnreachable.
//
// A host env is skipped before the port lookup, flagged as a host skip rather
// than one of the failure reasons: it has no pod, so it has no MCP edge at all
// and never will. It is still reported — as an informational line, not a
// warning — because an environment absent from the toolset otherwise reads as
// "not linked".
func buildOrchestratorMCPConfig(envs []eruncommon.OrchestratorEnvConfig, executable string, mcpPort mcpPortResolver, envType mcpEnvTypeResolver, reachable mcpReachabilityProber) (orchestratorMCPConfig, []orchestratorMCPSkip, []orchestratorMCPUnreachable) {
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
		// A host env has no pod, so no erun MCP edge runs for it and no
		// port-forward can ever reach one. Checked before the port lookup
		// because port allocation is type-blind: a host env does resolve a
		// port, so it would otherwise be wired and then reported as merely
		// unreachable — naming a deploy-or-reopen recovery that a host env
		// refuses outright.
		if envType != nil && envType(tenant, environment) == eruncommon.EnvironmentTypeHost {
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

// probeOrchestratorMCPEdges checks every wired env's edge concurrently, so N
// environments cost about one probe's worth of latency rather than N in
// series — this runs on every orchestrator launch and must never noticeably
// delay it. A nil prober (no seam wired) or nothing wired both answer "nothing
// unreachable" rather than probing.
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

// orchestratorEnvLabel names an environment for an operator-facing line, staying
// readable when the config entry itself is the thing that is malformed.
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

// The two ways an orchestrator ends up with linked environments but none of
// their tools. They stay distinct because the operator's fix differs: one is a
// missing erun install, the other a linked environment that no longer resolves.
var (
	errOrchestratorMCPExecutable = errors.New("the erun executable could not be resolved")
	errOrchestratorMCPNoPort     = errors.New("no linked environment resolved an MCP port")
)

// orchestratorMCPUnwiredNotice is the operator-facing line for an orchestrator
// that launched without the tools for the environments it is linked to. It names
// the cause and the matching recovery, since the session otherwise looks healthy
// right up to the first environment call.
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

// orchestratorMCPUnwiredAction names the control the frontend can render
// beside orchestratorMCPUnwiredNotice's message so its named recovery is
// something the operator can click rather than type: the executable-missing
// cause also links the install docs, since "install the erun command line
// tool" has no in-app affordance of its own.
func orchestratorMCPUnwiredAction(err error) string {
	if errors.Is(err, errOrchestratorMCPExecutable) {
		return notificationActionInstallAndRestartOrchestrator
	}
	return notificationActionRestartOrchestrator
}

// orchestratorMCPPartialNotice is the operator-facing line for an orchestrator
// that got SOME of its environments' tools. Distinct from the unwired notice
// because the session is usable and will look entirely healthy: the missing
// environment surfaces only as a tool that is not there, which an agent reads as
// "not linked" rather than "failed to wire".
//
// wired and total both count the orchestrator's LINKED environments, host ones
// included, so the sentence stays literally true. Counting only the environments
// that could have had tools is simpler and false: it makes a three-environment
// orchestrator read as a two-environment one. hostEnvs is taken only to name
// them, so the gap between wired and total is not left for the operator to
// account for.
func orchestratorMCPPartialNotice(name string, wired, total int, hostEnvs, skipped []orchestratorMCPSkip) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "The orchestrator"
	}
	missing := make([]string, 0, len(skipped))
	for _, skip := range skipped {
		missing = append(missing, fmt.Sprintf("%s (%s)", skip.Label, skip.Reason))
	}
	// Absent when no host env is linked, so the message an orchestrator without
	// one has always shown stays byte-for-byte unchanged.
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

// orchestratorMCPHostEnvNotice is the operator-facing line for an orchestrator
// linked to host environments, which have no MCP edge to wire. Informational
// rather than a warning: this is a working configuration, and the recovery is
// not to fix anything but to work in the environment's own directory. It is
// still said out loud because an environment absent from the toolset otherwise
// reads as "not linked" rather than "not applicable".
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

// orchestratorMCPUnreachableNotice is the operator-facing line for an
// orchestrator that wired an environment's tools even though its edge is not
// answering right now. Distinct from the partial notice above: that one names
// an environment left OUT of the toolset, this one names environments that ARE
// in it and will recover on their own once the edge comes back — the point is
// telling an orchestrator that finds an env's tools missing "linked, but the
// edge was down at launch" rather than leaving it to read as "not linked".
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

// singleOrchestratorMCPUnreachableEnv reports the one environment a deploy
// action can unambiguously target: exactly one unreachable env, whose Label
// is guaranteed "<tenant>/<environment>" (orchestratorEnvLabel's well-formed
// case — the only shape probeOrchestratorMCPEdges ever wires). More than one
// unreachable env has no single env to attach the action to.
func singleOrchestratorMCPUnreachableEnv(unreachable []orchestratorMCPUnreachable) (tenant, environment string, ok bool) {
	if len(unreachable) != 1 {
		return "", "", false
	}
	tenant, environment, found := strings.Cut(unreachable[0].Label, "/")
	if !found || tenant == "" || environment == "" {
		return "", "", false
	}
	return tenant, environment, true
}

// writeOrchestratorMCPConfig writes a per-orchestrator Claude Code --mcp-config
// file wiring each linked env's erun MCP into the orchestrator session, so it
// drives its envs through the MCP rather than raw kubectl. Returns "" with an
// error naming why when nothing could be wired, so the caller skips
// --mcp-config and can tell the operator which fix applies -- except when the
// only reason nothing wired is that every linked env is a host env, which is a
// working configuration and returns no error. Because a host env is a skip like
// any other, the caller gets every skip either way, host ones included, and can
// name what is absent instead of leaving it to read as "not linked".
func (a *App) writeOrchestratorMCPConfig(id string, envs []eruncommon.OrchestratorEnvConfig) (string, []orchestratorMCPSkip, []orchestratorMCPUnreachable, error) {
	// An orchestrator with no linked envs has nothing to wire, and that is normal.
	if len(envs) == 0 {
		return "", nil, nil, nil
	}
	// No erun binary means no proxy to launch, so the session launches without
	// its envs rather than with entries that would fail on first use.
	executable, err := eruncommon.ResolveErunExecutable()
	if err != nil {
		return "", nil, nil, fmt.Errorf("%w: %w", errOrchestratorMCPExecutable, err)
	}
	config, skipped, unreachable := buildOrchestratorMCPConfig(envs, executable,
		func(tenant, environment string) int {
			ports, portErr := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, tenant, environment)
			if portErr != nil {
				return 0
			}
			return ports.MCP
		},
		func(tenant, environment string) eruncommon.EnvironmentType {
			env, _, loadErr := a.deps.store.LoadEnvConfig(tenant, environment)
			if loadErr != nil {
				// An env whose config no longer resolves has no known type, so it
				// falls through to the port path and is reported as the wiring
				// failure it is -- never silently excused as a host env.
				return ""
			}
			return env.ResolvedType()
		},
		a.deps.canReachMCPEndpoint,
	)
	if len(config.MCPServers) == 0 {
		// Nothing wired because every linked env is a host env is a working
		// configuration, not a failure: a host env has no MCP edge by design and
		// the orchestrator drives it through its review directory. Only a real
		// failure to resolve a port takes the unwired-error path.
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

// orchestratorMCPConfigPath is a per-orchestrator sibling of
// orchestrator-restore.json under UserConfigDir()/ERun, so each orchestrator's
// env-MCP wiring is isolated (the shared orchestrators workspace can't hold a
// per-orchestrator .mcp.json).
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
