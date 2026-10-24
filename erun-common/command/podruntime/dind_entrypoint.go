package podruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/core"
)

// dindDockerdEntrypoint is the stock docker:dind entrypoint the sidecar hands
// over to once its own preparation is done. With a leading '-' in its first
// argument it prepends dockerd, so the args below reach the daemon.
const dindDockerdEntrypoint = "dockerd-entrypoint.sh"

// DindEntrypointParams carries the args the chart passes for dockerd.
type DindEntrypointParams struct {
	DockerdArgs []string
}

// dindEntrypointHost is everything RunDindEntrypoint reads from or hands over
// to the container it runs in. Linux wires the real procfs, sysfs, hostname
// and exec (dind_entrypoint_linux.go); tests wire stubs.
type dindEntrypointHost struct {
	procRoot string
	sysRoot  string
	hostname func() (string, error)
	lookPath func(string) (string, error)
	exec     func(argv0 string, argv []string, envv []string) error
	environ  func() []string
}

// RunDindEntrypoint starts the erun-dind sidecar's dockerd with a bridge MTU
// the pod's network can carry and with build containers CPU-capped under the
// sidecar's own quota, then replaces itself with the stock dind entrypoint.
//
// docker bridges containers at 1500; a CNI that encapsulates hands the pod
// less (a VXLAN overlay costs 50 bytes), and the difference silently
// black-holes every full-size packet a build fetches. The sidecar shares the
// pod's network namespace, so the real number is readable right here.
//
// Neither step is ever fatal: a daemon that starts at its defaults is strictly
// better than one that does not start. Each says so when it cannot apply.
func RunDindEntrypoint(ctx core.Context, params DindEntrypointParams) error {
	if hostOS := core.ResolveDetectedHostOS(); hostOS != core.HostOSLinux {
		return dindEntrypointUnsupportedError(string(hostOS))
	}
	host, err := defaultDindEntrypointHost()
	if err != nil {
		return err
	}
	return runDindEntrypoint(ctx, params, host)
}

func dindEntrypointUnsupportedError(goos string) error {
	return fmt.Errorf("erun dind-entrypoint runs only inside the erun-dind sidecar, which is Linux; this host is %s", goos)
}

func runDindEntrypoint(ctx core.Context, params DindEntrypointParams, host dindEntrypointHost) error {
	args := params.DockerdArgs
	// Reads procfs/sysfs rather than shelling out to iproute2, which the dind
	// image has no reason to carry (egressMTUUnder, shared with the build-side
	// MTU diagnosis that looks at the same namespace from the other side).
	if mtu, _, ok := eruncommon.EgressMTUUnder(host.procRoot, host.sysRoot); ok {
		ctx.Info(fmt.Sprintf("erun-dind: bridging containers at the pod network's MTU %d", mtu))
		args = append([]string{"--mtu=" + strconv.Itoa(mtu)}, args...)
	} else {
		ctx.Info("erun-dind: could not read the pod network MTU; leaving dockerd on its default")
	}
	capBuildContainerCPU(ctx, host)

	path, err := host.lookPath(dindDockerdEntrypoint)
	if err != nil {
		return fmt.Errorf("erun-dind: %s is not on PATH; erun dind-entrypoint runs only inside the erun-dind sidecar: %w", dindDockerdEntrypoint, err)
	}
	ctx.TraceCommand("", path, args...)
	if ctx.DryRun {
		return nil
	}
	// Nothing here anchors BuildKit's cache identity, on purpose. Every cache
	// record and cache mount on the docker-state volume is attributed to the
	// worker the daemon starts, and on the docker driver that worker takes its
	// id from the daemon's own engine id (moby's builder passes
	// `ID: opt.EngineID`), which dockerd writes to <data-root>/engine-id and
	// reuses on every later start. Measured on a rolled environment, every
	// record in buildkit/cache.db stayed keyed "<engine-id>::<ref>" across
	// repeated rolls, so the namespace is a property of the volume, not the
	// pod. What a roll does re-execute is the RUN steps: they are keyed under a
	// --cgroup-parent derived from the pod's hostname (build_cpu_cap.go).
	// <buildkit-root>/workerid is read only by standalone buildkitd's runc and
	// containerd workers, so writing one changes no key; recovering a lost id
	// from the records would mean scanning cache.db, which grows far past what
	// a container start may spend.
	return host.exec(path, append([]string{path}, args...), host.environ())
}

// capBuildContainerCPU mirrors this container's own cpu.max into a per-pod
// cgroup that `docker build --cgroup-parent` (buildContainerCPUCapCgroupParent)
// nests every build container under.
//
// Kubernetes declares the sidecar's CPU limit, but every container a real
// `docker build` creates lands as a sibling of the sidecar's own cgroup, not a
// descendant, so that limit never reaches them. The sidecar's own cgroup is one
// dockerd cannot escape: the kubelet assigned it before this runs, and its
// cpu.max carries the enforced quota. Keying the cap by the pod's hostname
// keeps two environments' sidecars sharing one node's cgroup tree apart.
//
// A cap that reads as applied while enforcing nothing is worse than a build
// openly left uncapped, so every way it cannot apply is reported, once.
func capBuildContainerCPU(ctx core.Context, host dindEntrypointHost) {
	ownCPUMax, pod, ok := readOwnCPUQuota(host)
	if !ok {
		return
	}
	capParent := filepath.Join(host.sysRoot, "fs", "cgroup", "docker")
	capCgroup := filepath.Join(capParent, "erun-build-cpu-cap-"+pod)
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("erun-dind: dry run: would delegate cpu via %s and mirror cpu.max %s into %s", filepath.Join(capParent, "cgroup.subtree_control"), ownCPUMax, capCgroup))
		return
	}
	if reason := delegateCPUController(capParent); reason != "" {
		reportUncappedBuilds(ctx, reason)
		return
	}
	if reason := writeCPUCap(capCgroup, ownCPUMax); reason != "" {
		reportUncappedBuilds(ctx, reason)
		return
	}
	ctx.Info(fmt.Sprintf("erun-dind: capping build containers via cgroup docker/erun-build-cpu-cap-%s (cpu.max %s)", pod, ownCPUMax))
}

// readOwnCPUQuota returns this container's own cpu.max and the pod name, and
// false whenever there is nothing to mirror: an unreadable cgroup, an
// unlimited quota, or no hostname to key the cap by.
func readOwnCPUQuota(host dindEntrypointHost) (string, string, bool) {
	ownCgroup := ownUnifiedCgroup(filepath.Join(host.procRoot, "self", "cgroup"))
	if ownCgroup == "" {
		return "", "", false
	}
	raw, err := os.ReadFile(filepath.Join(host.sysRoot, "fs", "cgroup", filepath.FromSlash(ownCgroup), "cpu.max"))
	if err != nil {
		return "", "", false
	}
	ownCPUMax := strings.TrimRight(string(raw), "\n")
	if ownCPUMax == "" || strings.HasPrefix(ownCPUMax, "max ") {
		return "", "", false
	}
	pod, err := host.hostname()
	if err != nil || strings.TrimSpace(pod) == "" {
		return "", "", false
	}
	return ownCPUMax, strings.TrimSpace(pod), true
}

// ownUnifiedCgroup returns the cgroup v2 path in a /proc/<pid>/cgroup file:
// the "0::<path>" line.
func ownUnifiedCgroup(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" {
			return parts[2]
		}
	}
	return ""
}

// delegateCPUController makes cpu available to capParent's children. cgroup v2
// creates a controller's interface files in a child only once the parent
// delegates that controller; until then the cap cgroup has no cpu.max at all,
// and writing it fails with an EACCES that looks like a capability problem.
// It returns why delegation is impossible, or "" once cpu is delegated.
func delegateCPUController(capParent string) string {
	if err := os.MkdirAll(capParent, 0o755); err != nil {
		return capParent + " could not be created"
	}
	controllers := filepath.Join(capParent, "cgroup.controllers")
	if !cgroupListsController(controllers, "cpu") {
		return controllers + " does not list cpu"
	}
	subtree := filepath.Join(capParent, "cgroup.subtree_control")
	if cgroupListsController(subtree, "cpu") {
		return ""
	}
	// Additive, so whatever else the parent delegates stays delegated, and
	// idempotent, so restarts are free. A refused delegation shows up below as
	// a cpu.max that cannot be written or does not read back.
	if err := os.WriteFile(subtree, []byte("+cpu\n"), 0o644); err != nil {
		return "cpu could not be delegated via " + subtree
	}
	return ""
}

// cgroupListsController reports whether name appears in a whitespace-separated
// controller list file; an unreadable or empty file lists none.
func cgroupListsController(path, name string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(data)) {
		if field == name {
			return true
		}
	}
	return false
}

// writeCPUCap writes the quota and reads it back: a write the kernel accepted
// is not proof the quota is in force. It returns why the cap is not in force,
// or "" once it is.
func writeCPUCap(capCgroup, cpuMax string) string {
	if err := os.MkdirAll(capCgroup, 0o755); err != nil {
		return capCgroup + " could not be created"
	}
	cpuMaxPath := filepath.Join(capCgroup, "cpu.max")
	if err := os.WriteFile(cpuMaxPath, []byte(cpuMax+"\n"), 0o644); err != nil {
		return cpuMaxPath + " could not be written"
	}
	// An unreadable cpu.max reads back as empty, which is never the quota.
	applied, _ := os.ReadFile(cpuMaxPath)
	if got := strings.TrimRight(string(applied), "\n"); got != cpuMax {
		return fmt.Sprintf("%s reads back '%s', not '%s'", cpuMaxPath, got, cpuMax)
	}
	return ""
}

func reportUncappedBuilds(ctx core.Context, reason string) {
	ctx.Info("erun-dind: WARNING: build containers are not CPU-capped: " + reason)
}
