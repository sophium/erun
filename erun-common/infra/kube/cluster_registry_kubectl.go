package kube

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// forwardingLinePattern matches kubectl's "Forwarding from 127.0.0.1:<port>" line.
var forwardingLinePattern = regexp.MustCompile(`127\.0\.0\.1:(\d+)`)

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

// lookupClusterIP returns a placeholder on --dry-run without touching the cluster.
func lookupClusterIP(ctx core.Context, kubeContext, namespace, service string) (string, error) {
	args := append(kubectlContextArgs(kubeContext, namespace), "get", "svc", service, "-o", "jsonpath={.spec.clusterIP}")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return "<cluster-ip>", nil
	}
	capture := ctx.ToolCapture()
	cmd := core.Command("kubectl", args...)
	cmd.Stderr = capture.Stderr()
	out, err := cmd.Output()
	if err != nil {
		return "", capture.Apply(fmt.Errorf("kubectl get svc %s/%s: %w", namespace, service, err))
	}
	return strings.TrimSpace(string(out)), nil
}

// ClusterRegistryForwards owns registry port-forwards; the starting command must Close it.
type ClusterRegistryForwards struct {
	procs []*exec.Cmd
}

// Start returns the local port kubectl chose; on --dry-run it returns remotePort without forwarding.
func (f *ClusterRegistryForwards) Start(ctx core.Context, kubeContext, namespace, service string, remotePort int) (int, error) {
	args := append(kubectlContextArgs(kubeContext, namespace), "port-forward", "svc/"+service, fmt.Sprintf(":%d", remotePort))
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return remotePort, nil
	}
	cmd := core.Command("kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	cmd.Stderr = ctx.Stderr
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start port-forward svc/%s: %w", service, err)
	}
	f.procs = append(f.procs, cmd)
	result := make(chan PortForwardResult, 1)
	go DrainPortForwardOutput(stdout, result)
	found := <-result
	if found.Err != nil {
		f.Close()
		return 0, fmt.Errorf("port-forward svc/%s: %w", service, found.Err)
	}
	return found.Port, nil
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

// PortForwardResult is the local port DrainPortForwardOutput found, or why it found none.
type PortForwardResult struct {
	Port int
	Err  error
}

// DrainPortForwardOutput reports the bound port, then keeps draining stdout for the
// process's life: a full 64KB pipe would block kubectl and stall the tunnel.
func DrainPortForwardOutput(stdout io.Reader, result chan<- PortForwardResult) {
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
		result <- PortForwardResult{Port: port, Err: err}
		reported = true
	}
	if reported {
		return
	}
	err := scanner.Err()
	if err == nil {
		err = fmt.Errorf("port-forward exited before reporting a local port")
	}
	result <- PortForwardResult{Err: err}
}

// ClusterRegistryDepsFor addresses the registry directly in-cluster, else via ClusterIP and port-forward.
func ClusterRegistryDepsFor(ctx core.Context, forwards core.RegistryForwarder) ClusterRegistryDeps {
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
