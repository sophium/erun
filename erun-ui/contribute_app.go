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

// contributeAppForward is the per-env kubectl port-forward that bridges the
// contribute clone's in-pod headless ERun app out to the developer's host,
// so they can open the locally-built desktop app in a browser without
// leaving the env.
type contributeAppForward struct {
	mu        sync.Mutex
	gen       int
	cmd       *exec.Cmd
	stderr    *bytes.Buffer
	localPort int
	exited    bool
	stopped   bool
}

func newContributeAppForward(cmd *exec.Cmd, stderr *bytes.Buffer, localPort int) *contributeAppForward {
	f := &contributeAppForward{cmd: cmd, stderr: stderr, localPort: localPort, gen: 1}
	f.startReap(1, cmd)
	return f
}

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

// adopt swaps in a freshly-spawned cmd. It returns false when stop() has
// already run concurrently; the caller must then kill the supplied cmd to
// avoid leaking a kubectl process the user asked to tear down.
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

// startReap publishes the eventual exit only while the forward is still on
// the same generation, so a slow Wait() for a cmd that adopt() has since
// swapped out cannot falsely mark the current one as exited.
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

// contributeAppPortReachableTimeout is generous on purpose: a cold in-pod
// build of erun-app (yarn install, the lint/typecheck gates, the vite
// bundle, a CGO build against webkit2gtk) takes 2-3 minutes, while
// incremental rebuilds are ~30s. 5 minutes covers both without letting a
// genuine failure (no listener, dead forward) hide behind the wait.
var (
	contributeAppPortReachableTimeout      = 5 * time.Minute
	contributeAppPortReachablePollInterval = 500 * time.Millisecond
)

// spawnContributeAppForwardCmd starts the kubectl port-forward, capturing
// stderr so a real error (missing deployment, unknown context, busy host
// port, pod with no listener) surfaces instead of just the reachability
// timeout. Reaping is owned by the forward, so this must not call Wait().
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

// waitForContributeAppReachable polls until the in-pod ERun app is serving
// HTTP, or the timeout expires. The check must be HTTP, not a plain TCP
// dial: kubectl port-forward accepts host-side TCP connections even when
// nothing in the pod is listening, then drops the whole forward with
// `error: lost connection to pod`. That almost always means the in-pod
// side is mid-rebuild and will bind seconds later, so the loop respawns
// kubectl on each exit before the deadline, preserving the last stderr so
// a genuine failure still surfaces if the app never binds.
//
// A nil forward with empty args is the reuse path — something is already
// listening locally — so the loop just polls HTTP and owns no kubectl.
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

// canReachLocalContributeAppEndpoint reports whether something in the pod is
// actually answering HTTP: any response (even a 404) proves a server is
// alive, while a refused connection or transport error means it is still
// booting or nothing is listening.
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

// canConnectLocalContributeAppPort is the TCP-only check for whether a
// port-forward is already alive, regardless of whether the pod-side server
// is serving — the distinction from the HTTP reach check.
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
