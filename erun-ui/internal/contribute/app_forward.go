package contribute

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

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/localports"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// appForward port-forwards the contribute clone's in-pod headless app to the host.
type appForward struct {
	mu        sync.Mutex
	gen       int
	cmd       *exec.Cmd
	stderr    *bytes.Buffer
	localPort int
	exited    bool
	stopped   bool
}

func newAppForward(cmd *exec.Cmd, stderr *bytes.Buffer, localPort int) *appForward {
	f := &appForward{cmd: cmd, stderr: stderr, localPort: localPort, gen: 1}
	f.startReap(1, cmd)
	return f
}

func (f *appForward) exitedWithError() (bool, string) {
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

// adopt returns false after stop(); the caller must then kill cmd.
func (f *appForward) adopt(cmd *exec.Cmd, stderr *bytes.Buffer) bool {
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

// startReap publishes the exit only for the current generation, so a stale Wait cannot mark a newer cmd exited.
func (f *appForward) startReap(gen int, cmd *exec.Cmd) {
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

// A cold in-pod erun-app build takes 2-3 minutes; incremental ~30s.
var (
	appReachableTimeout      = 5 * time.Minute
	appReachablePollInterval = 500 * time.Millisecond
)

// spawnForwardCmd captures stderr so real errors surface. The forward
// owns reaping; do not Wait here.
var spawnForwardCmd = func(ctx context.Context, args []string) (*exec.Cmd, *bytes.Buffer, error) {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	core.HideConsoleWindow(cmd)
	stderr := new(bytes.Buffer)
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start kubectl port-forward: %w", err)
	}
	return cmd, stderr, nil
}

func (s *Service) startAppForward(ctx context.Context, selection uimodel.Selection) (*appForward, []string, int, error) {
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("resolve environment: %w", err)
	}
	port := localports.ContributeAppPortForResult(result)
	if port <= 0 {
		return nil, nil, 0, fmt.Errorf("contribute-app port is not allocated for %s/%s", selection.Tenant, selection.Environment)
	}
	if canConnectLocalAppPort(port) {
		return nil, nil, port, nil
	}
	args := kubectlPortForwardArgs(result, port)
	cmd, stderr, err := spawnForwardCmd(ctx, args)
	if err != nil {
		return nil, nil, 0, err
	}
	return newAppForward(cmd, stderr, port), args, port, nil
}

func (f *appForward) stop() {
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
}

// waitForAppReachable polls HTTP, not TCP: kubectl port-forward accepts
// connections with no pod listener, then dies with "lost connection to pod" while
// the app rebuilds. It respawns kubectl until the deadline, keeping the last stderr.
// A nil forward with empty args means something already listens locally.
func waitForAppReachable(ctx context.Context, port int, forward *appForward, args []string) error {
	deadline := time.Now().Add(appReachableTimeout)
	var lastExitMsg string
	for time.Now().Before(deadline) {
		if canReachLocalAppEndpoint(port) {
			return nil
		}
		if forward != nil && len(args) > 0 {
			if err := respawnForwardIfExited(ctx, port, forward, args, &lastExitMsg); err != nil {
				return err
			}
		}
		time.Sleep(appReachablePollInterval)
	}
	if lastExitMsg != "" {
		return fmt.Errorf("contribute-app on 127.0.0.1:%d did not become reachable within %s (last kubectl exit: %s)", port, appReachableTimeout, lastExitMsg)
	}
	return fmt.Errorf("contribute-app on 127.0.0.1:%d did not become reachable within %s", port, appReachableTimeout)
}

func respawnForwardIfExited(ctx context.Context, port int, forward *appForward, args []string, lastExitMsg *string) error {
	exited, msg := forward.exitedWithError()
	if !exited {
		return nil
	}
	if msg != "" {
		*lastExitMsg = msg
	}
	cmd, stderr, spawnErr := spawnForwardCmd(ctx, args)
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

// canReachLocalAppEndpoint treats any HTTP response, even 404, as a live server.
func canReachLocalAppEndpoint(port int) bool {
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

// canConnectLocalAppPort checks only that the forward accepts TCP.
func canConnectLocalAppPort(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func kubectlPortForwardArgs(result config.OpenResult, localPort int) []string {
	args := make([]string, 0, 8)
	if c := strings.TrimSpace(result.EnvConfig.KubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	if ns := runtimetarget.KubernetesNamespaceName(result.Tenant, result.Environment); ns != "" {
		args = append(args, "--namespace", ns)
	}
	args = append(args,
		"port-forward",
		"deployment/"+runtimetarget.RuntimeReleaseName(result.Tenant),
		fmt.Sprintf("%d:%d", localPort, localPort),
		"--address", "127.0.0.1",
	)
	return args
}

type appForwards struct {
	mu       sync.Mutex
	forwards map[string]*appForward
}

func newAppForwards() *appForwards {
	return &appForwards{forwards: map[string]*appForward{}}
}

func (c *appForwards) put(selection uimodel.Selection, forward *appForward) {
	if c == nil || forward == nil {
		return
	}
	c.mu.Lock()
	c.forwards[forwardKey(selection)] = forward
	c.mu.Unlock()
}

func (c *appForwards) take(selection uimodel.Selection) *appForward {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := forwardKey(selection)
	forward := c.forwards[key]
	delete(c.forwards, key)
	return forward
}

func forwardKey(selection uimodel.Selection) string {
	selection = uimodel.NormalizeSelection(selection)
	return selection.Tenant + "/" + selection.Environment
}
