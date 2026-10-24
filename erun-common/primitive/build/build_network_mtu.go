package build

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

	"github.com/sophium/erun/erun-common/base/core"
)

// dockerBuildNetworkStallMarkers are transport failures only, never a server's answer: a
// 404 is a real build defect.
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

var warnOnceAboutBridgeMTU sync.Once

// warnAboutBridgeMTUMismatch warns rather than refuses: a CNI clamping MSS or a cached
// build can still succeed.
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

// dockerBuildNetworkDiagnosis claims an MTU cause only when a network stall coincides with
// a measured bridge/pod MTU mismatch.
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

func deployRemedyTarget() string {
	tenant := strings.TrimSpace(os.Getenv("ERUN_TENANT"))
	environment := strings.TrimSpace(os.Getenv("ERUN_ENVIRONMENT"))
	if tenant == "" || environment == "" {
		return "<tenant> <environment>"
	}
	return tenant + " " + environment
}

// dockerBridgeMTUMismatch is meaningful only in-pod: off-pod the daemon may be in a VM
// with unrelated interfaces.
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

// dockerDefaultBridgeMTU is read from the daemon, not assumed to be 1500.
func dockerDefaultBridgeMTU() (int, bool) {
	cmd := core.Command("docker", "network", "inspect", "bridge", "--format", `{{index .Options "com.docker.network.driver.mtu"}}`)
	out := new(bytes.Buffer)
	cmd.Stdout = out
	cmd.Stderr = new(bytes.Buffer)
	if err := cmd.Run(); err != nil {
		return 0, false
	}
	return parsePositiveMTU(strings.TrimSpace(out.String()))
}

// localEgressMTU reads procfs/sysfs, needing no iproute2 or privileges.
func localEgressMTU() (int, string, bool) {
	return EgressMTUUnder(procRootForMTU(), sysRootForMTU())
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

func EgressMTUUnder(procRoot, sysRoot string) (int, string, bool) {
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

// parsePositiveMTU rejects values below the IPv6 minimum as misreads.
func parsePositiveMTU(raw string) (int, bool) {
	mtu, err := strconv.Atoi(raw)
	if err != nil || mtu < 1280 {
		return 0, false
	}
	return mtu, true
}
