package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	activitypkg "github.com/sophium/erun/erun-common/infra/activity"
)

// One probe feeds the AI tab's session state and the Runtime tab's memory
// breakdown, so the two cannot contradict each other.

// runtimeActivityTimeout is short: a wedged pod must read as unavailable, not hang the UI.
const runtimeActivityTimeout = 10 * time.Second

const runtimeReclaimTimeout = 2 * time.Minute

// Group ids are stable keys for the frontend and for reclaim requests.
const (
	runtimeProcessGroupGradle = "gradle"
	runtimeProcessGroupJava   = "java"
	runtimeProcessGroupNode   = "node"
	runtimeProcessGroupDocker = "docker-build"
	runtimeProcessGroupOther  = "other"
)

// Reclaim actions never touch the worktree or a running session.
const (
	runtimeReclaimGradleDaemons = "gradle-daemons"
	runtimeReclaimBuildCache    = "build-cache"
)

// LoadRuntimeActivity reports the pod's sessions and memory-holding processes; it
// never reclaims and fails soft to an unavailable report.
func (a *App) LoadRuntimeActivity(selection uiSelection) (uiRuntimeActivity, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("load runtime activity", selection.Tenant, selection.Environment); err != nil {
		return uiRuntimeActivity{}, err
	}
	report, err := a.probeRuntimeActivity(selection)
	if err != nil {
		return uiRuntimeActivity{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
			Message:     fmt.Sprintf("Cannot read what the runtime is running: %v", err),
		}, nil
	}
	return report, nil
}

// ReclaimRuntimeResources runs one named reclaim action in the pod.
func (a *App) ReclaimRuntimeResources(input uiRuntimeReclaimInput) (uiRuntimeReclaimResult, error) {
	selection := normalizeSelection(uiSelection{Tenant: input.Tenant, Environment: input.Environment})
	if err := errMissingTenantOrEnvironment("reclaim runtime resources", selection.Tenant, selection.Environment); err != nil {
		return uiRuntimeReclaimResult{}, err
	}
	action := strings.TrimSpace(input.Action)
	script, err := runtimeReclaimScript(action)
	if err != nil {
		return uiRuntimeReclaimResult{}, err
	}
	ctx, cancel := context.WithTimeout(a.backgroundContext(), runtimeReclaimTimeout)
	defer cancel()
	if _, err := a.execInRuntimePod(ctx, selection, script); err != nil {
		return uiRuntimeReclaimResult{}, fmt.Errorf("%s: %w", runtimeReclaimLabel(action), err)
	}
	notice := fmt.Sprintf("%s in %s/%s. The figures below refresh with what is left.",
		runtimeReclaimDoneLabel(action), selection.Tenant, selection.Environment)
	a.emitAppNotification("info", notice)
	return uiRuntimeReclaimResult{Action: action, Message: notice}, nil
}

func (a *App) probeRuntimeActivity(selection uiSelection) (uiRuntimeActivity, error) {
	ctx, cancel := context.WithTimeout(a.backgroundContext(), runtimeActivityTimeout)
	defer cancel()
	output, err := a.execInRuntimePod(ctx, selection,
		activitypkg.RemoteAppSessionHeartbeatScript(selection.Tenant, selection.Environment)+"\n"+runtimeProcessSnapshotScript())
	if err != nil {
		return uiRuntimeActivity{}, errors.New(runtimeProbeFailureMessage(ctx, runtimeActivityTimeout, err, func(e error) string { return e.Error() }))
	}
	return runtimeActivityFromProbe(selection, output), nil
}

func (a *App) backgroundContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// execInRuntimePod uses kubectl exec rather than the MCP edge so it works while the edge is down.
func (a *App) execInRuntimePod(ctx context.Context, selection uiSelection, script string) (string, error) {
	if a.deps.execRuntimePod != nil {
		return a.deps.execRuntimePod(ctx, selection, script)
	}
	return "", fmt.Errorf("runtime pod exec is unavailable")
}

func execInRuntimePodViaKubectl(ctx context.Context, selection uiSelection, store erunUIStore, script string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("configuration store is unavailable")
	}
	envConfig, _, err := store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return "", err
	}
	kubernetesContext := strings.TrimSpace(envConfig.KubernetesContext)
	if kubernetesContext == "" {
		return "", fmt.Errorf("environment has no kubernetes context")
	}
	return kubectlText(ctx, kubernetesContext,
		"--namespace", config.KubernetesNamespaceName(selection.Tenant, selection.Environment),
		"exec",
		"deployment/"+config.RuntimeReleaseName(selection.Tenant),
		"--",
		"/bin/sh", "-c", script,
	)
}

const runtimeProcessSnapshotPrefix = "erun-process\t"

func runtimeProcessSnapshotScript() string {
	return "ps -eo rss=,comm=,args= 2>/dev/null | while read -r rss comm args; do " +
		"printf '" + runtimeProcessSnapshotPrefix + "%s\\t%s\\t%s\\n' \"$rss\" \"$comm\" \"$args\"; done"
}

func runtimeActivityFromProbe(selection uiSelection, output string) uiRuntimeActivity {
	heartbeats := activitypkg.ParseRemoteAppSessionHeartbeats(output)
	sessions := make([]uiRuntimeSession, 0, len(heartbeats))
	for _, heartbeat := range heartbeats {
		sessions = append(sessions, uiRuntimeSession{
			ID:      heartbeat.ID,
			Running: heartbeat.Running(),
			Program: heartbeat.Program,
		})
	}
	groups, totalMi := runtimeProcessGroupsFromProbe(output)
	activity := uiRuntimeActivity{
		Tenant:          selection.Tenant,
		Environment:     selection.Environment,
		Available:       true,
		Sessions:        sessions,
		SessionsRunning: activitypkg.RunningRemoteAppSessions(heartbeats),
		Processes:       groups,
		MemoryHeldMiB:   totalMi,
		MemoryHeld:      formatMebibytes(totalMi),
	}
	activity.Message = runtimeActivityMessage(activity)
	return activity
}

func runtimeActivityMessage(activity uiRuntimeActivity) string {
	sessions := "no sessions running"
	switch activity.SessionsRunning {
	case 1:
		sessions = "1 session running"
	default:
		if activity.SessionsRunning > 1 {
			sessions = fmt.Sprintf("%d sessions running", activity.SessionsRunning)
		}
	}
	leftover := len(activity.Sessions) - activity.SessionsRunning
	if leftover > 0 {
		sessions += fmt.Sprintf(" (%s no longer has a program behind it)", pluralizeSockets(leftover))
	}
	return fmt.Sprintf("Live in the pod right now: %s, %s held by running processes.", sessions, activity.MemoryHeld)
}

func pluralizeSockets(count int) string {
	if count == 1 {
		return "1 socket"
	}
	return fmt.Sprintf("%d sockets", count)
}

func runtimeProcessGroupsFromProbe(output string) ([]uiRuntimeProcessGroup, int64) {
	type accumulator struct {
		count int
		rssKi int64
	}
	totals := map[string]*accumulator{}
	var totalKi int64
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if !strings.HasPrefix(line, runtimeProcessSnapshotPrefix) {
			continue
		}
		fields := strings.SplitN(strings.TrimPrefix(line, runtimeProcessSnapshotPrefix), "\t", 3)
		if len(fields) < 2 {
			continue
		}
		rssKi, err := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
		if err != nil {
			continue
		}
		args := ""
		if len(fields) > 2 {
			args = fields[2]
		}
		group := runtimeProcessGroupFor(strings.TrimSpace(fields[1]), args)
		if group == "" {
			continue
		}
		totalKi += rssKi
		if totals[group] == nil {
			totals[group] = &accumulator{}
		}
		totals[group].count++
		totals[group].rssKi += rssKi
	}
	groups := make([]uiRuntimeProcessGroup, 0, len(totals))
	for id, totals := range totals {
		memoryMi := totals.rssKi / 1024
		groups = append(groups, uiRuntimeProcessGroup{
			ID:           id,
			Label:        runtimeProcessGroupLabel(id, totals.count),
			Count:        totals.count,
			MemoryMiB:    memoryMi,
			Memory:       formatMebibytes(memoryMi),
			Reclaim:      runtimeProcessGroupReclaim(id),
			ReclaimLabel: runtimeReclaimLabel(runtimeProcessGroupReclaim(id)),
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].MemoryMiB != groups[j].MemoryMiB {
			return groups[i].MemoryMiB > groups[j].MemoryMiB
		}
		return groups[i].ID < groups[j].ID
	})
	return groups, totalKi / 1024
}

// Matched on a comm prefix: the memory holder can carry a suffixed name (e.g. claude.exe).
// Unlisted processes (sh, ps, sshd, sleep) are not reclaimable and are dropped.
var runtimeProcessGroupByCommPrefix = map[string]string{
	"buildkit": runtimeProcessGroupDocker,
	"claude":   runtimeProcessGroupOther,
	"codex":    runtimeProcessGroupOther,
}

var runtimeProcessGroupByCommand = map[string]string{
	"java":       runtimeProcessGroupJava,
	"node":       runtimeProcessGroupNode,
	"dockerd":    runtimeProcessGroupDocker,
	"containerd": runtimeProcessGroupDocker,
}

func runtimeProcessGroupFor(comm, args string) string {
	if strings.Contains(args, "GradleDaemon") || strings.Contains(args, "gradle") {
		return runtimeProcessGroupGradle
	}
	for prefix, group := range runtimeProcessGroupByCommPrefix {
		if strings.HasPrefix(comm, prefix) {
			return group
		}
	}
	return runtimeProcessGroupByCommand[comm]
}

func runtimeProcessGroupLabel(id string, count int) string {
	switch id {
	case runtimeProcessGroupGradle:
		return pluralizeProcesses(count, "Gradle daemon")
	case runtimeProcessGroupJava:
		return pluralizeProcesses(count, "Java process")
	case runtimeProcessGroupNode:
		return pluralizeProcesses(count, "Node process")
	case runtimeProcessGroupDocker:
		return pluralizeProcesses(count, "container build process")
	default:
		return pluralizeProcesses(count, "agent process")
	}
}

func pluralizeProcesses(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// An agent process is the operator's work, so it has no reclaim action.
func runtimeProcessGroupReclaim(id string) string {
	switch id {
	case runtimeProcessGroupGradle, runtimeProcessGroupJava:
		return runtimeReclaimGradleDaemons
	case runtimeProcessGroupDocker:
		return runtimeReclaimBuildCache
	default:
		return ""
	}
}

func runtimeReclaimLabel(action string) string {
	switch action {
	case runtimeReclaimGradleDaemons:
		return "Stop build daemons"
	case runtimeReclaimBuildCache:
		return "Prune build cache"
	default:
		return ""
	}
}

func runtimeReclaimDoneLabel(action string) string {
	switch action {
	case runtimeReclaimGradleDaemons:
		return "Stopped the build daemons"
	case runtimeReclaimBuildCache:
		return "Pruned the build cache"
	default:
		return "Reclaimed resources"
	}
}

func runtimeReclaimScript(action string) (string, error) {
	switch action {
	case runtimeReclaimGradleDaemons:
		// `Gradle[D]aemon` keeps `pgrep -f` from matching (and killing) this script's own shell.
		return strings.Join([]string{
			"if command -v gradle >/dev/null 2>&1; then gradle --stop >/dev/null 2>&1 || true; fi",
			"for pid in $(pgrep -f 'Gradle[D]aemon' 2>/dev/null || true); do [ \"$pid\" = \"$$\" ] || kill \"$pid\" 2>/dev/null || true; done",
		}, "\n"), nil
	case runtimeReclaimBuildCache:
		return "docker buildx prune -f >/dev/null 2>&1 || docker builder prune -f >/dev/null 2>&1 || true\ndocker image prune -f >/dev/null 2>&1 || true", nil
	default:
		return "", fmt.Errorf("unknown reclaim action %q", action)
	}
}

func formatMebibytes(mebibytes int64) string {
	if mebibytes <= 0 {
		return "0 MiB"
	}
	if mebibytes < 1024 {
		return fmt.Sprintf("%d MiB", mebibytes)
	}
	return fmt.Sprintf("%.1f GiB", round1(float64(mebibytes)/1024))
}
