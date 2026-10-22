package eruncommon

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// buildDaemonEndpointAddr is where a Dockerfile test stage's
// `RUN --network=host` steps reach a Docker daemon: the loopback listener an
// environment pod's dind sidecar provides.
const buildDaemonEndpointAddr = "127.0.0.1:2375"

// buildDaemonForwarderImage forwards that address to the build's own daemon
// socket where nothing listens there already.
const buildDaemonForwarderImage = "alpine/socat:1.8.0.3"

const (
	buildDaemonForwarderName = "erun-build-daemon"
	buildDaemonOwnerLabel    = "erun.build-daemon.owner-pid"
)

// buildDaemonEndpointAnswers reports whether something already listens on the
// endpoint in this process's network. Inside an environment pod erun shares the
// pod's network with the dind sidecar, so its listener answers here; on a host
// the builder's loopback is the daemon host's own, which this process cannot
// see, and the probe is simply false.
var buildDaemonEndpointAnswers = func() bool {
	conn, err := net.DialTimeout("tcp", buildDaemonEndpointAddr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

var runBuildDaemonCommand = func(args ...string) (string, error) {
	output, err := Command("docker", args...).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// ensureBuildDaemonEndpoint makes the test-stage daemon endpoint exist for the
// duration of a build that has a test stage to run.
//
// The endpoint is part of the test-stage contract, not a property of one
// venue: a test stage that starts a container fixture reaches a daemon at
// buildDaemonEndpointAddr. An environment pod provides it through its dind
// sidecar. Anywhere else nothing does, so the build failed minutes in with a
// daemon connection error. Here the build's own daemon is forwarded to that
// address on the daemon host's loopback -- the network a `RUN --network=host`
// step joins -- and the forwarder is removed when the build ends.
func ensureBuildDaemonEndpoint(ctx Context, builds []DockerBuildSpec) (func(), error) {
	noop := func() {}
	if !anyBuildDeclaresTestStage(builds) {
		return noop, nil
	}
	// The dry run states the contract rather than probing, so its trace is the
	// same on every machine.
	if ctx.DryRun {
		ctx.Trace("test-stage daemon endpoint " + buildDaemonEndpointAddr + ": used as is where it answers, otherwise forwarded from this build's daemon")
		ctx.TraceCommand("", "docker", buildDaemonForwarderArgs("<pid>")...)
		return noop, nil
	}
	// One forwarder per daemon, not per build: the address can be bound only
	// once. A forwarder whose owning build is still running is shared and left
	// to that build; one whose owner is gone (a build that was killed before it
	// could clean up) is replaced. This comes before the probe because a
	// leftover forwarder answers it too -- on Docker Desktop and OrbStack a
	// host-network port is also published on the Mac's own loopback -- and would
	// otherwise be used and never removed.
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

// buildDaemonForwarderOwner reads the pid label of an existing forwarder,
// whether one exists, and whether it is running.
func buildDaemonForwarderOwner() (owner string, exists, running bool) {
	out, err := runBuildDaemonCommand("inspect", "-f", `{{index .Config.Labels "`+buildDaemonOwnerLabel+`"}} {{.State.Running}}`, buildDaemonForwarderName)
	if err != nil {
		return "", false, false
	}
	owner, state, _ := strings.Cut(strings.TrimSpace(out), " ")
	return owner, true, state == "true"
}

// buildDaemonOwnerAlive reports whether the build that started a forwarder is
// still running on this machine.
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

// waitBuildDaemonForwarderRunning catches a forwarder that exited at once --
// typically because the address is already bound on the daemon host by
// something this process cannot see -- so the build fails here, naming it,
// rather than in a test stage minutes later.
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

// buildDaemonForwarderArgs runs the forwarder on the daemon host's network,
// bound to loopback only, so nothing beyond that host reaches the daemon.
func buildDaemonForwarderArgs(ownerPID string) []string {
	return []string{
		"run", "-d", "--name", buildDaemonForwarderName, "--label", buildDaemonOwnerLabel + "=" + ownerPID, "--network", "host",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		buildDaemonForwarderImage,
		"TCP-LISTEN:2375,bind=127.0.0.1,reuseaddr,fork", "UNIX-CONNECT:/var/run/docker.sock",
	}
}

func anyBuildDeclaresTestStage(builds []DockerBuildSpec) bool {
	for _, build := range builds {
		if dockerfileDeclaresTestStage(build.DockerfilePath) {
			return true
		}
	}
	return false
}
