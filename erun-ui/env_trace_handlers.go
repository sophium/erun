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

// uiEnvTrace is the Diagnostics console's "erun trace" read model (issues
// #466/#508): the tail of the env's persistent trace log — capture is
// always on — or an honest reason why there is nothing to show.
type uiEnvTrace struct {
	Available bool   `json:"available"`
	Content   string `json:"content,omitempty"`
	Path      string `json:"path"`
	Reason    string `json:"reason,omitempty"`
}

// LoadEnvTrace reads the selected env's trace log for the
// Diagnostics console: the host file for local-agent envs, the in-pod file
// (over the env's MCP port-forward, gated on reachability) for remote envs.
// Read-only and side-effect free — reading diagnostics must never mutate
// the env or hold it awake (the pod read carries the idle-probe header).
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
	if !result.EnvConfig.RemoteWorktree() {
		return a.loadHostEnvTrace(result, trace), nil
	}
	return a.loadPodEnvTrace(result, trace), nil
}

func (a *App) loadHostEnvTrace(result eruncommon.OpenResult, trace uiEnvTrace) uiEnvTrace {
	path, err := eruncommon.EnvTraceLogPath(result.Tenant, result.Environment)
	if err != nil {
		trace.Reason = err.Error()
		return trace
	}
	content, err := tailFile(path, envTraceTailBytes)
	if err != nil {
		if os.IsNotExist(err) {
			trace.Reason = "no trace captured yet"
			return trace
		}
		trace.Reason = err.Error()
		return trace
	}
	trace.Available = true
	trace.Content = content
	return trace
}

func (a *App) loadPodEnvTrace(result eruncommon.OpenResult, trace uiEnvTrace) uiEnvTrace {
	mcpPort := eruncommon.MCPPortForResult(result)
	if mcpPort <= 0 || !a.deps.canConnectLocalPort(mcpPort) {
		trace.Reason = "environment is not reachable — open it to read the in-pod trace"
		return trace
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := a.deps.runPodRaw(ctx, mcpEndpointForOpenResult(result), []string{
		"sh", "-c",
		fmt.Sprintf("tail -c %d \"$HOME/.erun/%s/%s/trace.log\" 2>/dev/null || true", envTraceTailBytes, result.Tenant, result.Environment),
	})
	if err != nil {
		trace.Reason = "reading the in-pod trace failed: " + err.Error()
		return trace
	}
	if strings.TrimSpace(out) == "" {
		trace.Reason = "no trace captured yet"
		return trace
	}
	trace.Available = true
	trace.Content = out
	return trace
}

// tailFile returns up to maxBytes from the end of path, starting at the
// first complete line when the file was truncated mid-line.
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
