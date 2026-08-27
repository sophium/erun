package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// runtime_activity.go answers one question the desktop could not answer before:
// what is this environment's runtime pod actually running right now. Two
// consumers need it and both were previously guessing. The AI tab inferred
// "still working" from the last stream event, which is wrong in both directions
// — a quiet session looks dead and a dropped exec stream looks alive. And the
// Runtime tab showed an environment's resource figures with nothing explaining
// why they were heavy, because build daemons that outlive their build hold
// memory invisibly.
//
// One probe answers both, so the session count, the running state the UI
// animates, and the processes blamed for the memory all come from the same
// observation and cannot contradict each other.

// runtimeActivityTimeout bounds the pod probe. It is a status read on a UI
// path, so a wedged pod must surface as "unavailable" quickly rather than
// hanging the Runtime tab or the heartbeat poller.
const runtimeActivityTimeout = 10 * time.Second

// runtimeReclaimTimeout is longer: stopping Gradle daemons and pruning the
// build cache are real work in the pod, not a read.
const runtimeReclaimTimeout = 2 * time.Minute

// runtimeProcessGroup ids are stable so the frontend keys on the group, not its
// prose, and so a reclaim request names what it is reclaiming.
const (
	runtimeProcessGroupGradle = "gradle"
	runtimeProcessGroupJava   = "java"
	runtimeProcessGroupNode   = "node"
	runtimeProcessGroupDocker = "docker-build"
	runtimeProcessGroupOther  = "other"
)

// Reclaim actions. Both are deliberately narrow and named for what the operator
// recognises; neither touches the worktree or a running session.
const (
	runtimeReclaimGradleDaemons = "gradle-daemons"
	runtimeReclaimBuildCache    = "build-cache"
)

// LoadRuntimeActivity reports what the environment's runtime pod is running:
// its persistent desktop sessions with the program behind each, and the
// processes holding memory grouped by what an operator would recognise.
// Read-only — nothing here reclaims anything.
//
// Fail-soft in the same way the rest of the pod reads are: an unreachable or
// stopped environment yields an unavailable report with the reason, never an
// error that turns the Runtime tab into a failure surface.
func (a *App) LoadRuntimeActivity(selection uiSelection) (uiRuntimeActivity, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiRuntimeActivity{}, fmt.Errorf("tenant and environment are required")
	}
	report, err := a.probeRuntimeActivity(selection)
	if err != nil {
		return uiRuntimeActivity{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
			Message:     "Cannot read what the runtime is running: " + err.Error(),
		}, nil
	}
	return report, nil
}

// ReclaimRuntimeResources runs one named reclaim action in the pod. Explicit
// action only: LoadRuntimeActivity never reclaims, so the operator always sees
// what is there before anything is stopped.
func (a *App) ReclaimRuntimeResources(input uiRuntimeReclaimInput) (uiRuntimeReclaimResult, error) {
	selection := normalizeSelection(uiSelection{Tenant: input.Tenant, Environment: input.Environment})
	if selection.Tenant == "" || selection.Environment == "" {
		return uiRuntimeReclaimResult{}, fmt.Errorf("tenant and environment are required")
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

// probeRuntimeActivity runs the single read the two consumers share.
func (a *App) probeRuntimeActivity(selection uiSelection) (uiRuntimeActivity, error) {
	ctx, cancel := context.WithTimeout(a.backgroundContext(), runtimeActivityTimeout)
	defer cancel()
	output, err := a.execInRuntimePod(ctx, selection,
		eruncommon.RemoteAppSessionHeartbeatScript(selection.Tenant, selection.Environment)+"\n"+runtimeProcessSnapshotScript())
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

// execInRuntimePod runs a script in the env's runtime container over kubectl
// exec. kubectl rather than the MCP edge on purpose: this read must work while
// the edge is down or no port-forward is bound, which is exactly when a session
// is most likely to look stale.
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
		"--namespace", eruncommon.KubernetesNamespaceName(selection.Tenant, selection.Environment),
		"exec",
		"deployment/"+eruncommon.RuntimeReleaseName(selection.Tenant),
		"--",
		"/bin/sh", "-c", script,
	)
}

// runtimeProcessSnapshotPrefix tags the process lines so one probe can carry
// both the session heartbeats and the process snapshot.
const runtimeProcessSnapshotPrefix = "erun-process\t"

// runtimeProcessSnapshotScript reports every process in the pod with its
// resident memory. RSS rather than a container-level total because the point is
// to name what is holding the memory: an operator seeing "13 Java processes,
// 5.4 GiB" can act, where a single container figure only says "heavy".
func runtimeProcessSnapshotScript() string {
	return "ps -eo rss=,comm=,args= 2>/dev/null | while read -r rss comm args; do " +
		"printf '" + runtimeProcessSnapshotPrefix + "%s\\t%s\\t%s\\n' \"$rss\" \"$comm\" \"$args\"; done"
}

func runtimeActivityFromProbe(selection uiSelection, output string) uiRuntimeActivity {
	heartbeats := eruncommon.ParseRemoteAppSessionHeartbeats(output)
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
		SessionsRunning: eruncommon.RunningRemoteAppSessions(heartbeats),
		Processes:       groups,
		MemoryHeldMiB:   totalMi,
		MemoryHeld:      formatMebibytes(totalMi),
	}
	activity.Message = runtimeActivityMessage(activity)
	return activity
}

// runtimeActivityMessage states the observation in one line, so the panel reads
// as a live reading of the pod rather than a static inventory.
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

// runtimeProcessGroupsFromProbe folds the raw process list into the handful of
// groups an operator can act on, largest first. Grouping rather than listing
// pids: "13 Java processes from Gradle" is a decision, a pid table is homework.
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

// runtimeProcessGroupFor classifies one process. Only processes worth an
// operator's attention are grouped; the pod's own supervision (sh, ps, sshd,
// the entrypoint's sleep) is dropped so the panel is a list of things that
// could be reclaimed rather than a process table.
// Matched on a comm prefix, not an exact name: the image wraps the AI tools, so
// the process that actually holds the memory is `claude-real`, not `claude`, and
// an exact match reported the agent as consuming nothing.
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

// runtimeProcessGroupReclaim maps a group to the action that reclaims it, or
// empty when there is no safe reclaim — an agent process is the operator's
// work, not a leftover, so the panel shows it without offering to kill it.
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

// runtimeReclaimScript returns the pod-side script for a named action. Both
// actions are scoped to build leftovers: neither touches the worktree, a
// running desktop session, or the AI tool.
func runtimeReclaimScript(action string) (string, error) {
	switch action {
	case runtimeReclaimGradleDaemons:
		// Gradle's own stop first so a daemon gets to shut down cleanly, then
		// the JVMs it left behind. Both tolerate absence — a pod with no Gradle
		// is a successful no-op, not an error.
		//
		// The bracket in `Gradle[D]aemon` keeps the pattern from matching the
		// shell running this script: `pgrep -f` reads whole command lines, and
		// this script's own command line contains the pattern, so a literal
		// spelling makes the reclaim kill itself (observed as exit 143 against a
		// live pod). The regex matches `GradleDaemon`; the literal text
		// `Gradle[D]aemon` in our own argv does not match it.
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
