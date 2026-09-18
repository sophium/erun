package eruncommon

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// forwardingLinePattern extracts the local port kubectl chose from its
// "Forwarding from 127.0.0.1:<port> -> <remote>" startup line.
var forwardingLinePattern = regexp.MustCompile(`127\.0\.0\.1:(\d+)`)

// kubectlContextArgs prefixes namespace/context flags shared by the registry
// kubectl calls.
func kubectlContextArgs(kubeContext, namespace string) []string {
	args := make([]string, 0, 4)
	if strings.TrimSpace(kubeContext) != "" {
		args = append(args, "--context", kubeContext)
	}
	if strings.TrimSpace(namespace) != "" {
		args = append(args, "-n", namespace)
	}
	return args
}

// lookupClusterIP resolves the registry Service's ClusterIP. On --dry-run it
// traces the query and returns a placeholder so the resolved address stays
// deterministic without touching the cluster.
func lookupClusterIP(ctx Context, kubeContext, namespace, service string) (string, error) {
	args := append(kubectlContextArgs(kubeContext, namespace), "get", "svc", service, "-o", "jsonpath={.spec.clusterIP}")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return "<cluster-ip>", nil
	}
	capture := ctx.ToolCapture()
	cmd := Command("kubectl", args...)
	cmd.Stderr = capture.Stderr()
	out, err := cmd.Output()
	if err != nil {
		return "", capture.Apply(fmt.Errorf("kubectl get svc %s/%s: %w", namespace, service, err))
	}
	return strings.TrimSpace(string(out)), nil
}

// ClusterRegistryForwards owns the kubectl port-forward processes started to
// reach an in-cluster registry from the host. The command that starts them must
// Close it when the build/push/publish finishes so no forward leaks.
type ClusterRegistryForwards struct {
	procs []*exec.Cmd
}

// Start forwards a free local port to svc/<service>:<remotePort> and returns the
// port kubectl chose. On --dry-run it traces the command and returns the remote
// port as a representative local port without forwarding.
func (f *ClusterRegistryForwards) Start(ctx Context, kubeContext, namespace, service string, remotePort int) (int, error) {
	args := append(kubectlContextArgs(kubeContext, namespace), "port-forward", "svc/"+service, fmt.Sprintf(":%d", remotePort))
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return remotePort, nil
	}
	cmd := Command("kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	cmd.Stderr = ctx.Stderr
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start port-forward svc/%s: %w", service, err)
	}
	f.procs = append(f.procs, cmd)
	result := make(chan portForwardResult, 1)
	go drainPortForwardOutput(stdout, result)
	found := <-result
	if found.err != nil {
		f.Close()
		return 0, fmt.Errorf("port-forward svc/%s: %w", service, found.err)
	}
	return found.port, nil
}

// Close terminates every forward this manager started.
func (f *ClusterRegistryForwards) Close() {
	for _, cmd := range f.procs {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	f.procs = nil
}

// portForwardResult is what drainPortForwardOutput reports back once it has
// found (or failed to find) the local port kubectl bound.
type portForwardResult struct {
	port int
	err  error
}

// drainPortForwardOutput scans kubectl port-forward's stdout for the local
// port it bound, reports it once, and then keeps reading for the rest of the
// process's life so a long-running forward's later "Handling connection for
// ..." lines never fill the pipe. Stopping after the port line — as this used
// to do by returning from the scan loop early — leaves nothing draining
// stdout for a forward that can run for the whole build/push; once kubectl's
// own 64KB pipe buffer fills, it blocks writing and the tunnel it was
// providing stalls silently -- a parent that stops draining a live child's
// output, one hop further down the stack than the docker build/push path this
// same defect shape was found in. The loop ends on its own once the pipe
// closes (kubectl exits or Close kills it), so no goroutine is leaked.
func drainPortForwardOutput(stdout io.Reader, result chan<- portForwardResult) {
	scanner := bufio.NewScanner(stdout)
	reported := false
	for scanner.Scan() {
		if reported {
			continue
		}
		m := forwardingLinePattern.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		port, err := strconv.Atoi(m[1])
		result <- portForwardResult{port: port, err: err}
		reported = true
	}
	if reported {
		return
	}
	err := scanner.Err()
	if err == nil {
		err = fmt.Errorf("port-forward exited before reporting a local port")
	}
	result <- portForwardResult{err: err}
}

// ClusterRegistryDepsFor builds the live resolver dependencies: an in-cluster
// build addresses the registry directly; a host build looks up the ClusterIP and
// port-forwards through the given manager.
func ClusterRegistryDepsFor(ctx Context, forwards *ClusterRegistryForwards) ClusterRegistryDeps {
	return ClusterRegistryDeps{
		InCluster: RunningInCluster(),
		LookupClusterIP: func(kubeContext, namespace, service string) (string, error) {
			return lookupClusterIP(ctx, kubeContext, namespace, service)
		},
		StartPortForward: func(kubeContext, namespace, service string, remotePort int) (int, error) {
			return forwards.Start(ctx, kubeContext, namespace, service, remotePort)
		},
	}
}
