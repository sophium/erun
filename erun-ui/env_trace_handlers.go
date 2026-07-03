package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// envTraceTailBytes bounds how much of the per-env trace log one console
// refresh transfers — enough scrollback to diagnose, small enough to poll.
const envTraceTailBytes = 64 * 1024

// uiEnvTrace is the Diagnostics console's "erun trace" read model: the tail
// of the env's always-on trace log, or an honest reason there is nothing to
// show.
type uiEnvTrace struct {
	Available bool   `json:"available"`
	Content   string `json:"content,omitempty"`
	Path      string `json:"path"`
	Reason    string `json:"reason,omitempty"`
	Notice    string `json:"notice,omitempty"`
}

// LoadEnvTrace reads the selected env's trace log for the Diagnostics
// console. A remote env is merged from two vantage points: the host log
// records operator-driven commands run from this machine, the in-pod log
// records agent-driven MCP actions. Reading diagnostics must never mutate
// the env or hold it awake.
func (a *App) LoadEnvTrace(selection uiSelection) (uiEnvTrace, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiEnvTrace{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uiEnvTrace{}, err
	}
	trace := uiEnvTrace{
		Path: "~/.erun/" + result.Tenant + "/" + result.Environment + "/trace.log",
	}
	content, hostIssue := hostEnvTraceTail(result)
	if result.EnvConfig.RemoteWorktree() {
		podContent, notice := a.podEnvTraceTail(result)
		content = mergeStampedTraces(content, podContent)
		trace.Notice = joinTraceNotices(notice, hostIssue)
	} else {
		trace.Notice = hostIssue
	}
	if strings.TrimSpace(content) == "" {
		trace.Reason = "no trace captured yet"
		return trace, nil
	}
	trace.Available = true
	trace.Content = content
	return trace, nil
}

// A missing file is the normal "nothing ran yet" state, not an error; only
// real read failures return an issue string, so a failed host read never
// blanks the pod vantage point.
func hostEnvTraceTail(result eruncommon.OpenResult) (string, string) {
	path, err := eruncommon.EnvTraceLogPath(result.Tenant, result.Environment)
	if err != nil {
		return "", err.Error()
	}
	content, err := tailFile(path, envTraceTailBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ""
		}
		return "", "reading the host trace failed: " + err.Error()
	}
	return content, ""
}

// Unreachable or failed pod reads degrade to a notice instead of blanking
// the pane, so the host side still renders.
func (a *App) podEnvTraceTail(result eruncommon.OpenResult) (string, string) {
	mcpPort := eruncommon.MCPPortForResult(result)
	if mcpPort <= 0 || !a.deps.canConnectLocalPort(mcpPort) {
		return "", "in-pod trace unavailable — open the environment to include it"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := a.deps.runPodRaw(ctx, mcpEndpointForOpenResult(result), a.mcpBearer(result.Tenant, result.EnvConfig.Name), []string{
		"sh", "-c",
		fmt.Sprintf("tail -c %d \"$HOME/.erun/%s/%s/trace.log\" 2>/dev/null || true", envTraceTailBytes, result.Tenant, result.Environment),
	})
	if err != nil {
		return "", "reading the in-pod trace failed: " + err.Error()
	}
	return out, ""
}

// RFC3339 UTC stamps make lexicographic order chronological, so comparing
// stamps as strings interleaves the host and pod tails in time order; an
// unstamped continuation line inherits its predecessor's stamp to stay
// attached.
func mergeStampedTraces(host, pod string) string {
	hostLines := splitTraceLines(host)
	podLines := splitTraceLines(pod)
	if len(podLines) == 0 {
		return host
	}
	for i := range podLines {
		podLines[i].text = markPodTraceLine(podLines[i].text)
	}
	if len(hostLines) == 0 {
		return joinTraceLines(podLines)
	}
	merged := make([]stampedTraceLine, 0, len(hostLines)+len(podLines))
	h, p := 0, 0
	for h < len(hostLines) && p < len(podLines) {
		if hostLines[h].stamp <= podLines[p].stamp {
			merged = append(merged, hostLines[h])
			h++
		} else {
			merged = append(merged, podLines[p])
			p++
		}
	}
	merged = append(merged, hostLines[h:]...)
	merged = append(merged, podLines[p:]...)
	return joinTraceLines(merged)
}

type stampedTraceLine struct {
	stamp string
	text  string
}

func splitTraceLines(content string) []stampedTraceLine {
	content = strings.TrimRight(content, "\n")
	if strings.TrimSpace(content) == "" {
		return nil
	}
	rawLines := strings.Split(content, "\n")
	lines := make([]stampedTraceLine, 0, len(rawLines))
	lastStamp := ""
	for _, line := range rawLines {
		if stamp, ok := traceLineStamp(line); ok {
			lastStamp = stamp
		}
		lines = append(lines, stampedTraceLine{stamp: lastStamp, text: line})
	}
	return lines
}

func traceLineStamp(line string) (string, bool) {
	stamp, _, found := strings.Cut(line, " ")
	if !found {
		return "", false
	}
	if _, err := time.Parse(time.RFC3339, stamp); err != nil {
		return "", false
	}
	return stamp, true
}

func markPodTraceLine(line string) string {
	if stamp, ok := traceLineStamp(line); ok {
		return stamp + " [pod]" + line[len(stamp):]
	}
	return "[pod] " + line
}

func joinTraceLines(lines []stampedTraceLine) string {
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		texts = append(texts, line.text)
	}
	return strings.Join(texts, "\n") + "\n"
}

func joinTraceNotices(notices ...string) string {
	parts := make([]string, 0, len(notices))
	for _, notice := range notices {
		if strings.TrimSpace(notice) != "" {
			parts = append(parts, notice)
		}
	}
	return strings.Join(parts, "; ")
}

func tailFile(path string, maxBytes int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	offset := info.Size() - maxBytes
	truncated := offset > 0
	if !truncated {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	content := string(raw)
	if truncated {
		if idx := strings.IndexByte(content, '\n'); idx >= 0 {
			content = content[idx+1:]
		}
	}
	return content, nil
}
