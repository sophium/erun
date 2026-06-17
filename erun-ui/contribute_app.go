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
	mu        sync.Mutex
	gen       int
	cmd       *exec.Cmd
	stderr    *bytes.Buffer
	localPort int
	exited    bool
	stopped   bool
}

// newContributeAppForward wires the initial kubectl cmd into a fresh
// forward and starts the reap goroutine that records its exit so
// exitedWithError can read it race-free under f.mu.
func newContributeAppForward(cmd *exec.Cmd, stderr *bytes.Buffer, localPort int) *contributeAppForward {
	f := &contributeAppForward{cmd: cmd, stderr: stderr, localPort: localPort, gen: 1}
	f.startReap(1, cmd)
	return f
}

// exitedWithError reports whether the kubectl process currently held
// by the forward has already terminated and, if so, returns the
// captured stderr. The exit signal is published under f.mu by
// startReap, so reads here are race-free against the cmd's I/O
// teardown.
func (f *contributeAppForward) exitedWithError() (bool, string) {
	if f == nil {
		return false, ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.exited {
		return false, ""
	}
	msg := ""
	if f.stderr != nil {
		msg = strings.TrimSpace(f.stderr.String())
	}
	return true, msg
}

// adopt swaps in a freshly-spawned kubectl cmd and stderr buffer as
// the forward's current process. It returns false when stop() has
// already been called concurrently — in that case the caller must
// kill the supplied cmd so we don't leak a kubectl process the user
// has asked to tear down. The generation counter ensures the previous
// cmd's late reap (it may exit after adopt swaps it out) does not
// mark the new cmd as exited.
func (f *contributeAppForward) adopt(cmd *exec.Cmd, stderr *bytes.Buffer) bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return false
	}
	f.gen++
	gen := f.gen
	f.cmd = cmd
	f.stderr = stderr
	f.exited = false
	f.mu.Unlock()
	f.startReap(gen, cmd)
	return true
}

// startReap blocks until cmd exits, then publishes the exit under
// f.mu — but only if the forward is still on the same generation. A
// later adopt() bumps gen, so a slow Wait() return for an old cmd
// does not falsely mark the current cmd as exited.
func (f *contributeAppForward) startReap(gen int, cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		_ = cmd.Wait()
		f.mu.Lock()
		if f.gen == gen {
			f.exited = true
		}
		f.mu.Unlock()
	}()
}

// contributeAppPortReachableTimeout is generous on purpose. The first
// in-pod build of erun-app does yarn install, the lint/typecheck/format
// gates (unless ERUN_SKIP_LINT=1 is honored by the prelude), the vite
// frontend bundle, then a CGO Go build linking against webkit2gtk. On
// a cold pod that's 2-3 minutes; subsequent incremental builds are
// ~30s. 5 minutes leaves headroom for both without being so long that
// a genuine failure (no listener, port-forward dead) hides behind it.
//
// Declared as vars (rather than consts) so tests can shrink them.
var (
	contributeAppPortReachableTimeout      = 5 * time.Minute
	contributeAppPortReachablePollInterval = 500 * time.Millisecond
)

// spawnContributeAppForwardCmd builds and starts the kubectl
// port-forward process. kubectl's stderr is captured so a real
// error (no such deployment, context not in kubeconfig, host port
// busy, refused-by-pod-with-no-listener) surfaces instead of just
// the 5-minute reachability timeout. Reaping is owned by the
// forward (newContributeAppForward / adopt call startReap); the
// spawn function deliberately does not call Wait() so test fakes
// stay symmetric with the production shape. Exposed as a var so
// tests can substitute a fake.
var spawnContributeAppForwardCmd = func(ctx context.Context, args []string) (*exec.Cmd, *bytes.Buffer, error) {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	stderr := new(bytes.Buffer)
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start kubectl port-forward: %w", err)
	}
	return cmd, stderr, nil
}

func (a *App) startContributeAppForward(ctx context.Context, selection uiSelection) (*contributeAppForward, []string, int, error) {
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("resolve environment: %w", err)
	}
	port := eruncommon.ContributeAppPortForResult(result)
	if port <= 0 {
		return nil, nil, 0, fmt.Errorf("contribute-app port is not allocated for %s/%s", selection.Tenant, selection.Environment)
	}
	// If something is already listening locally, reuse it. The most
	// common case is a still-running forward from a previous click.
	if canConnectLocalContributeAppPort(port) {
		return nil, nil, port, nil
	}
	args := kubectlContributeAppPortForwardArgs(result, port)
	cmd, stderr, err := spawnContributeAppForwardCmd(ctx, args)
	if err != nil {
		return nil, nil, 0, err
	}
	return newContributeAppForward(cmd, stderr, port), args, port, nil
}

func (f *contributeAppForward) stop() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.stopped = true
	cmd := f.cmd
	f.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	// startReap has the Wait() goroutine; no need to Wait() here.
}

// waitForContributeAppReachable polls the local port until the headless
// ERun app is actually serving HTTP, or the timeout expires. Using HTTP
// (instead of plain TCP dial) is essential because kubectl port-forward
// happily accepts TCP connections on the host even when nothing inside
// the pod is listening — the proxied connection then drops the whole
// forward with `error: lost connection to pod`. When that happens the
// in-pod side is almost always still mid-rebuild and will bind a few
// seconds later, so a fresh kubectl spawned right after will succeed.
//
// The loop therefore respawns kubectl whenever the forward exits and
// the deadline has not yet expired, preserving the last captured
// stderr so a genuine failure (no such deployment, host port busy,
// context missing) still surfaces if the in-pod app never binds.
//
// `forward` is nil and `args` is empty in the reuse path (something is
// already listening on the local port) — the loop then simply polls
// HTTP without owning any kubectl lifecycle.
func waitForContributeAppReachable(ctx context.Context, port int, forward *contributeAppForward, args []string) error {
	deadline := time.Now().Add(contributeAppPortReachableTimeout)
	var lastExitMsg string
	for time.Now().Before(deadline) {
		if canReachLocalContributeAppEndpoint(port) {
			return nil
		}
		if forward != nil && len(args) > 0 {
			if err := respawnContributeAppForwardIfExited(ctx, port, forward, args, &lastExitMsg); err != nil {
				return err
			}
		}
		time.Sleep(contributeAppPortReachablePollInterval)
	}
	if lastExitMsg != "" {
		return fmt.Errorf("contribute-app on 127.0.0.1:%d did not become reachable within %s (last kubectl exit: %s)", port, contributeAppPortReachableTimeout, lastExitMsg)
	}
	return fmt.Errorf("contribute-app on 127.0.0.1:%d did not become reachable within %s", port, contributeAppPortReachableTimeout)
}

// respawnContributeAppForwardIfExited checks whether the forward's kubectl
// process has already exited and, if so, spawns a fresh one and adopts it into
// the forward. It records the most recent captured stderr in *lastExitMsg so a
// genuine failure still surfaces if the in-pod app never binds. A nil return
// means either the forward is still alive or a respawn succeeded; a non-nil
// return is a terminal error that aborts the reachability wait.
func respawnContributeAppForwardIfExited(ctx context.Context, port int, forward *contributeAppForward, args []string, lastExitMsg *string) error {
	exited, msg := forward.exitedWithError()
	if !exited {
		return nil
	}
	if msg != "" {
		*lastExitMsg = msg
	}
	cmd, stderr, spawnErr := spawnContributeAppForwardCmd(ctx, args)
	if spawnErr != nil {
		return fmt.Errorf("respawn kubectl port-forward for 127.0.0.1:%d after early exit (%s): %w", port, lastExitMsgOrPlaceholder(*lastExitMsg), spawnErr)
	}
	if !forward.adopt(cmd, stderr) {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return fmt.Errorf("contribute-app port-forward for 127.0.0.1:%d was stopped before becoming reachable", port)
	}
	return nil
}

func lastExitMsgOrPlaceholder(msg string) string {
	if msg == "" {
		return "(no stderr captured)"
	}
	return msg
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
