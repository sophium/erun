package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// contributeAppForward is the per-env desktop-owned kubectl port-forward
// process that bridges the contribute clone's headless `erun app` HTTP
// server (running inside the env's pod on port = LocalPorts.ContributeApp)
// out to the developer's host, so the user can open the locally-built
// ERun desktop app in their browser without leaving the env.
//
// Lifecycle:
//   - started by App.StartContributeApp (the "Open contribute app" button).
//   - stopped by App.stopContributeAppForward on contribute-mode-off,
//     env switch, or app shutdown.
type contributeAppForward struct {
	cmd       *exec.Cmd
	localPort int
	stderr    *bytes.Buffer
}

// exitedWithError reports whether the kubectl process has already
// terminated and, if so, returns the captured stderr. Used by
// waitForContributeAppReachable to fail fast instead of polling the
// HTTP port for 5 minutes when kubectl is already dead.
func (f *contributeAppForward) exitedWithError() (bool, string) {
	if f == nil || f.cmd == nil || f.cmd.Process == nil {
		return false, ""
	}
	if f.cmd.ProcessState == nil {
		return false, ""
	}
	msg := strings.TrimSpace(f.stderr.String())
	return true, msg
}

// contributeAppPortReachableTimeout is generous on purpose. The first
// in-pod build of erun-app does yarn install, the lint/typecheck/format
// gates (unless ERUN_SKIP_LINT=1 is honored by the prelude), the vite
// frontend bundle, then a CGO Go build linking against webkit2gtk. On
// a cold pod that's 2-3 minutes; subsequent incremental builds are
// ~30s. 5 minutes leaves headroom for both without being so long that
// a genuine failure (no listener, port-forward dead) hides behind it.
const contributeAppPortReachableTimeout = 5 * time.Minute

func (a *App) startContributeAppForward(ctx context.Context, selection uiSelection) (*contributeAppForward, int, error) {
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("resolve environment: %w", err)
	}
	port := eruncommon.ContributeAppPortForResult(result)
	if port <= 0 {
		return nil, 0, fmt.Errorf("contribute-app port is not allocated for %s/%s", selection.Tenant, selection.Environment)
	}
	// If something is already listening locally, reuse it. The most
	// common case is a still-running forward from a previous click.
	if canConnectLocalContributeAppPort(port) {
		return nil, port, nil
	}
	args := kubectlContributeAppPortForwardArgs(result, port)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	stderr := new(bytes.Buffer)
	cmd.Stdout = io.Discard
	// Capture kubectl's stderr so we can surface a real error message
	// when it exits early (no such deployment, context not in kubeconfig,
	// port already in use on host, etc.) instead of staring at the
	// 5-minute reachability timeout.
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("start kubectl port-forward: %w", err)
	}
	forward := &contributeAppForward{cmd: cmd, localPort: port, stderr: stderr}
	// Reap the process in a background goroutine so cmd.ProcessState is
	// populated when (if) kubectl exits. exitedWithError() can then
	// detect a dead forward without blocking the caller.
	go func() {
		_ = cmd.Wait()
	}()
	return forward, port, nil
}

func (f *contributeAppForward) stop() {
	if f == nil || f.cmd == nil || f.cmd.Process == nil {
		return
	}
	_ = f.cmd.Process.Kill()
	// Wait is already reaped by the goroutine spawned in
	// startContributeAppForward; calling it again here is a no-op.
}

// waitForContributeAppReachable polls the local port until the headless
// ERun app is actually serving HTTP, or the timeout expires. Using HTTP
// (instead of plain TCP dial) is essential because kubectl port-forward
// happily accepts TCP connections on the host even when nothing inside
// the pod is listening — the proxied connection then drops. The
// headless boot includes a Wails frontend build on first run, which
// can take a while, so the timeout is generous.
//
// If forward.exitedWithError() reports kubectl is already dead the
// wait fails immediately with kubectl's stderr so the user sees the
// real cause instead of a generic 5-minute timeout.
func waitForContributeAppReachable(port int, forward *contributeAppForward) error {
	deadline := time.Now().Add(contributeAppPortReachableTimeout)
	for time.Now().Before(deadline) {
		if canReachLocalContributeAppEndpoint(port) {
			return nil
		}
		if exited, msg := forward.exitedWithError(); exited {
			if msg == "" {
				msg = "(no stderr captured)"
			}
			return fmt.Errorf("kubectl port-forward for 127.0.0.1:%d exited before the headless app was reachable: %s", port, msg)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("contribute-app on 127.0.0.1:%d did not become reachable within %s", port, contributeAppPortReachableTimeout)
}

// canReachLocalContributeAppEndpoint reports whether the host-side
// port-forward is wired up AND something inside the pod is actually
// answering HTTP. A 2xx/3xx/4xx response (including "404 not found")
// proves an HTTP server is alive; a refused connection or transport
// error means we're still booting / nothing is listening.
func canReachLocalContributeAppEndpoint(port int) bool {
	if port <= 0 {
		return false
	}
	client := http.Client{Timeout: 750 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// canConnectLocalContributeAppPort is the TCP-only check used to
// detect whether a kubectl port-forward is already alive (regardless
// of whether the pod-side server is serving). Reused by reach-checks
// where we only care about the forward, not the HTTP endpoint.
func canConnectLocalContributeAppPort(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func kubectlContributeAppPortForwardArgs(result eruncommon.OpenResult, localPort int) []string {
	args := make([]string, 0, 8)
	if c := strings.TrimSpace(result.EnvConfig.KubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	if ns := eruncommon.KubernetesNamespaceName(result.Tenant, result.Environment); ns != "" {
		args = append(args, "--namespace", ns)
	}
	args = append(args,
		"port-forward",
		"deployment/"+eruncommon.RuntimeReleaseName(result.Tenant),
		fmt.Sprintf("%d:%d", localPort, localPort),
		"--address", "127.0.0.1",
	)
	return args
}

// contributeAppForwards tracks the active port-forward process per env
// (keyed by tenant/env). The map is owned by App; mutations go through
// the helpers below to keep locking centralised.
type contributeAppForwards struct {
	mu       sync.Mutex
	forwards map[string]*contributeAppForward
}

func newContributeAppForwards() *contributeAppForwards {
	return &contributeAppForwards{forwards: map[string]*contributeAppForward{}}
}

func (c *contributeAppForwards) put(selection uiSelection, forward *contributeAppForward) {
	if c == nil || forward == nil {
		return
	}
	c.mu.Lock()
	c.forwards[contributeAppForwardKey(selection)] = forward
	c.mu.Unlock()
}

func (c *contributeAppForwards) take(selection uiSelection) *contributeAppForward {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := contributeAppForwardKey(selection)
	forward := c.forwards[key]
	delete(c.forwards, key)
	return forward
}

func contributeAppForwardKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return selection.Tenant + "/" + selection.Environment
}
