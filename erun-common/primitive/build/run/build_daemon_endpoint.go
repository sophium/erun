package run

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// buildDaemonEndpointAddr is where a test stage's `RUN --network=host` reaches a daemon.
const buildDaemonEndpointAddr = "127.0.0.1:2375"

const buildDaemonForwarderImage = "alpine/socat:1.8.0.3"

const (
	buildDaemonForwarderName = "erun-build-daemon"
	buildDaemonOwnerLabel    = "erun.build-daemon.owner-pid"
)

// buildDaemonEndpointAnswers is true in an environment pod (shared network with dind) and false on a host.
var buildDaemonEndpointAnswers = func() bool {
	conn, err := net.DialTimeout("tcp", buildDaemonEndpointAddr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

var runBuildDaemonCommand = func(args ...string) (string, error) {
	output, err := core.Command("docker", args...).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// ensureBuildDaemonEndpoint forwards the build's daemon to buildDaemonEndpointAddr on the
// daemon host's loopback when nothing provides it, for the build's duration.
func ensureBuildDaemonEndpoint(ctx core.Context, builds []imagebuild.DockerBuildSpec) (func(), error) {
	noop := func() {}
	if !anyBuildDeclaresTestStage(builds) {
		return noop, nil
	}
	if ctx.DryRun {
		ctx.Trace("test-stage daemon endpoint " + buildDaemonEndpointAddr + ": used as is where it answers, otherwise forwarded from this build's daemon")
		ctx.TraceCommand("", "docker", buildDaemonForwarderArgs("<pid>")...)
		return noop, nil
	}
	// One forwarder per daemon: the address binds once. Checked before the probe because a
	// leftover forwarder also answers it (Docker Desktop/OrbStack publish host ports locally).
	owner, exists, running := buildDaemonForwarderOwner()
	if running && buildDaemonOwnerAlive(owner) {
		ctx.Trace("test-stage daemon endpoint " + buildDaemonEndpointAddr + ": forwarded by a running build (pid " + owner + "), sharing it")
		return noop, nil
	}
	if exists {
		_, _ = runBuildDaemonCommand("rm", "-f", buildDaemonForwarderName)
	} else if buildDaemonEndpointAnswers() {
		ctx.Trace("test-stage daemon endpoint " + buildDaemonEndpointAddr + ": answers, using it")
		return noop, nil
	}
	args := buildDaemonForwarderArgs(strconv.Itoa(os.Getpid()))
	ctx.Trace("test-stage daemon endpoint " + buildDaemonEndpointAddr + ": nothing answers, forwarding this build's daemon there")
	ctx.TraceCommand("", "docker", args...)
	if output, err := runBuildDaemonCommand(args...); err != nil {
		return nil, fmt.Errorf("forward the build's Docker daemon to %s for the test stage: %w: %s", buildDaemonEndpointAddr, err, output)
	}
	stop := func() { _, _ = runBuildDaemonCommand("rm", "-f", buildDaemonForwarderName) }
	if err := waitBuildDaemonForwarderRunning(buildDaemonForwarderName); err != nil {
		stop()
		return nil, err
	}
	return stop, nil
}

func buildDaemonForwarderOwner() (owner string, exists, running bool) {
	out, err := runBuildDaemonCommand("inspect", "-f", `{{index .Config.Labels "`+buildDaemonOwnerLabel+`"}} {{.State.Running}}`, buildDaemonForwarderName)
	if err != nil {
		return "", false, false
	}
	owner, state, _ := strings.Cut(strings.TrimSpace(out), " ")
	return owner, true, state == "true"
}

var buildDaemonOwnerAlive = func(pid string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return false
	}
	process, err := os.FindProcess(n)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// waitBuildDaemonForwarderRunning fails the build now if the forwarder exits at once.
func waitBuildDaemonForwarderRunning(name string) error {
	var state, logs string
	for i := 0; i < 10; i++ {
		state, _ = runBuildDaemonCommand("inspect", "-f", "{{.State.Running}}", name)
		if state != "true" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if state == "true" {
		return nil
	}
	logs, _ = runBuildDaemonCommand("logs", name)
	return fmt.Errorf("the forwarder for the test-stage daemon endpoint %s exited: %s", buildDaemonEndpointAddr, logs)
}

// buildDaemonForwarderArgs binds to loopback only, so nothing beyond the daemon host reaches it.
func buildDaemonForwarderArgs(ownerPID string) []string {
	return []string{
		"run", "-d", "--name", buildDaemonForwarderName, "--label", buildDaemonOwnerLabel + "=" + ownerPID, "--network", "host",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		buildDaemonForwarderImage,
		"TCP-LISTEN:2375,bind=127.0.0.1,reuseaddr,fork", "UNIX-CONNECT:/var/run/docker.sock",
	}
}

func anyBuildDeclaresTestStage(builds []imagebuild.DockerBuildSpec) bool {
	for _, build := range builds {
		if dockerspec.DockerfileDeclaresTestStage(build.DockerfilePath) {
			return true
		}
	}
	return false
}
