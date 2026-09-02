package eruncommon

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// dockerBuildNetworkStallMarkers are the tells that a build step died reaching
// the network rather than on its own merits. They are deliberately about the
// *transport* giving up -- a connection reset, a receive that never completed,
// a handshake that timed out -- and not about a server's answer: a 404 from a
// pinned download URL is a real defect in the build, and must never be
// attributed to the pod's network.
//
// The signature of the mismatch this file diagnoses is peculiar and worth
// naming: the TCP connection establishes, small packets flow, and the transfer
// then stops dead at the first full-size one. What a tool reports for that is
// either a stall it eventually gives up on or a reset, so both appear here.
var dockerBuildNetworkStallMarkers = []string{
	"recv failure",
	"connection reset by peer",
	"ssl connection timeout",
	"gnutls recv error",
	"tls handshake timeout",
	"operation timed out after",
	"connection timed out after",
	"failed to fetch",
	"temporary failure resolving",
}

// warnOnceAboutBridgeMTU keeps the warning to one line per process. A build
// resolves many images and would otherwise repeat it for each.
var warnOnceAboutBridgeMTU sync.Once

// warnAboutBridgeMTUMismatch says up front that this environment's daemon is
// bridging wider than its pod network can carry, so the operator learns it in
// the first seconds rather than from a download that stalls many minutes in.
//
// A warning rather than a refusal, deliberately: the mismatch makes failure
// likely, not certain -- a CNI that clamps MSS masks it, and a fully-cached
// build never touches the network at all. Refusing here would fail builds that
// would have succeeded, which for a required merge gate is the same disease as
// the bug it is warning about. The refusal-grade certainty only exists once a
// step has actually stalled, which is where dockerBuildNetworkDiagnosis speaks.
func warnAboutBridgeMTUMismatch(stderr io.Writer) {
	if stderr == nil {
		return
	}
	warnOnceAboutBridgeMTU.Do(func() {
		mismatch, ok := dockerBridgeMTUMismatch()
		if !ok {
			return
		}
		_, _ = io.WriteString(stderr, "warning: "+mismatch.diagnosis()+"\n")
	})
}

// dockerBuildNetworkDiagnosis explains a failed build step whose output shows
// the network gave up, when this pod's docker daemon is bridging containers at
// an MTU its own pod network cannot carry. Both halves are required: the
// mismatch alone does not prove breakage (some CNIs clamp MSS and paper over
// it), and a network stall alone has plenty of other causes. Only when a real
// stall coincides with a real, measured mismatch does this claim a cause --
// per root AGENTS.md, a confident remedy for a cause that was not checked is
// worse than none.
func dockerBuildNetworkDiagnosis(output string) (string, bool) {
	if !dockerBuildOutputShowsNetworkStall(output) {
		return "", false
	}
	mismatch, ok := dockerBridgeMTUMismatch()
	if !ok {
		return "", false
	}
	return mismatch.diagnosis(), true
}

func dockerBuildOutputShowsNetworkStall(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range dockerBuildNetworkStallMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// bridgeMTUMismatch is a measured pair, never an assumed one: both numbers were
// read back, and BridgeMTU exceeds PodMTU.
type bridgeMTUMismatch struct {
	BridgeMTU int
	PodMTU    int
	Interface string
}

func (m bridgeMTUMismatch) diagnosis() string {
	return fmt.Sprintf(
		"this build's docker daemon bridges containers at MTU %d, but the pod network it egresses through (%s) carries only %d. "+
			"Oversized replies are dropped with no ICMP the overlay lets back, so a download stalls at the first full-size packet "+
			"and reports it as the connection failing -- the code being built is not at fault. "+
			"Redeploy this environment so the dind sidecar picks up the MTU it derives from the pod network:\n"+
			"  erun deploy %s",
		m.BridgeMTU, m.Interface, m.PodMTU, deployRemedyTarget(),
	)
}

// deployRemedyTarget names this environment in the remedy when the pod knows
// what it is, so the suggested command is runnable rather than a template the
// reader has to fill in.
func deployRemedyTarget() string {
	tenant := strings.TrimSpace(os.Getenv("ERUN_TENANT"))
	environment := strings.TrimSpace(os.Getenv("ERUN_ENVIRONMENT"))
	if tenant == "" || environment == "" {
		return "<tenant> <environment>"
	}
	return tenant + " " + environment
}

// dockerBridgeMTUMismatch compares the MTU the local docker daemon bridges
// containers at against the MTU of the interface this process egresses
// through. ok is false unless both were read and the bridge is genuinely the
// larger -- an unreadable value, or a daemon whose bridge already fits, is not
// a mismatch.
//
// Only meaningful in-pod, which is the only place the two share a network
// namespace: off-pod the daemon may live in a VM (Docker Desktop) whose
// interfaces have nothing to do with this process's, so the comparison would be
// between unrelated numbers. Gated accordingly.
func dockerBridgeMTUMismatch() (bridgeMTUMismatch, bool) {
	return dockerBridgeMTUMismatchWith(os.Getenv, dockerDefaultBridgeMTU, localEgressMTU)
}

func dockerBridgeMTUMismatchWith(
	getenv func(string) string,
	bridgeMTU func() (int, bool),
	egressMTU func() (int, string, bool),
) (bridgeMTUMismatch, bool) {
	if getenv == nil || strings.TrimSpace(getenv("KUBERNETES_SERVICE_HOST")) == "" {
		return bridgeMTUMismatch{}, false
	}
	bridge, ok := bridgeMTU()
	if !ok {
		return bridgeMTUMismatch{}, false
	}
	pod, iface, ok := egressMTU()
	if !ok || bridge <= pod {
		return bridgeMTUMismatch{}, false
	}
	return bridgeMTUMismatch{BridgeMTU: bridge, PodMTU: pod, Interface: iface}, true
}

// dockerDefaultBridgeMTU reports the MTU the daemon hands containers on its
// default bridge -- the network BuildKit's own steps run on. Read back from the
// daemon rather than assumed to be docker's 1500 default, so a daemon already
// started with --mtu is correctly seen as fine.
func dockerDefaultBridgeMTU() (int, bool) {
	cmd := Command("docker", "network", "inspect", "bridge", "--format", `{{index .Options "com.docker.network.driver.mtu"}}`)
	out := new(bytes.Buffer)
	cmd.Stdout = out
	cmd.Stderr = new(bytes.Buffer)
	if err := cmd.Run(); err != nil {
		return 0, false
	}
	return parsePositiveMTU(strings.TrimSpace(out.String()))
}

// localEgressMTU reports the MTU of the interface carrying this process's
// default route, and its name. Reads procfs/sysfs directly so it needs no
// iproute2 and no privileges, matching what the dind entrypoint wrapper does
// from the other side of the same namespace.
func localEgressMTU() (int, string, bool) {
	return egressMTUUnder(procRootForMTU(), sysRootForMTU())
}

func procRootForMTU() string {
	if root := strings.TrimSpace(os.Getenv("ERUN_PROC_ROOT")); root != "" {
		return root
	}
	return "/proc"
}

func sysRootForMTU() string {
	if root := strings.TrimSpace(os.Getenv("ERUN_SYS_ROOT")); root != "" {
		return root
	}
	return "/sys"
}

func egressMTUUnder(procRoot, sysRoot string) (int, string, bool) {
	iface, ok := defaultRouteInterface(filepath.Join(procRoot, "net", "route"))
	if !ok {
		return 0, "", false
	}
	raw, err := os.ReadFile(filepath.Join(sysRoot, "class", "net", iface, "mtu"))
	if err != nil {
		return 0, "", false
	}
	mtu, ok := parsePositiveMTU(strings.TrimSpace(string(raw)))
	if !ok {
		return 0, "", false
	}
	return mtu, iface, true
}

// defaultRouteInterface picks the interface whose route has an all-zero
// destination and mask, which is what a default route looks like in
// /proc/net/route.
func defaultRouteInterface(routeTable string) (string, bool) {
	file, err := os.Open(routeTable)
	if err != nil {
		return "", false
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 {
			continue
		}
		if fields[1] == "00000000" && fields[7] == "00000000" {
			return fields[0], true
		}
	}
	return "", false
}

// parsePositiveMTU rejects anything below the IPv6 minimum: a smaller number is
// a misread rather than a link this code should reason about.
func parsePositiveMTU(raw string) (int, bool) {
	mtu, err := strconv.Atoi(raw)
	if err != nil || mtu < 1280 {
		return 0, false
	}
	return mtu, true
}
