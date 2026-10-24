package podruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/build"
)

// dindDockerdEntrypoint prepends dockerd when its first argument starts with '-'.
const dindDockerdEntrypoint = "dockerd-entrypoint.sh"

type dindEntrypointHost struct {
	procRoot string
	sysRoot  string
	hostname func() (string, error)
	lookPath func(string) (string, error)
	exec     func(argv0 string, argv []string, envv []string) error
	environ  func() []string
}

// RunDindEntrypoint sets dockerd's bridge MTU to what the pod network carries and caps
// build container CPU, then execs the stock entrypoint. Neither step is fatal.
func RunDindEntrypoint(ctx core.Context, params model.DindEntrypointParams) error {
	if hostOS := core.ResolveDetectedHostOS(); hostOS != model.HostOSLinux {
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

func runDindEntrypoint(ctx core.Context, params model.DindEntrypointParams, host dindEntrypointHost) error {
	args := params.DockerdArgs
	// The dind image has no iproute2, so read procfs/sysfs.
	if mtu, _, ok := build.EgressMTUUnder(host.procRoot, host.sysRoot); ok {
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
	// BuildKit's cache identity needs no anchoring: it follows the engine-id dockerd persists
	// in its data-root on the docker-state volume.
	return host.exec(path, append([]string{path}, args...), host.environ())
}

// capBuildContainerCPU mirrors this container's cpu.max into a per-pod cgroup that build
// containers nest under; they are otherwise siblings outside the sidecar's limit.
// Every failure to apply is reported so an uncapped build is never silent.
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

// ownUnifiedCgroup returns the path on the "0::<path>" line.
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

// delegateCPUController is required before the child cgroup has a cpu.max at all;
// otherwise the write fails with a misleading EACCES.
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
	if err := os.WriteFile(subtree, []byte("+cpu\n"), 0o644); err != nil {
		return "cpu could not be delegated via " + subtree
	}
	return ""
}

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

// writeCPUCap reads the quota back: an accepted write is not proof it is in force.
func writeCPUCap(capCgroup, cpuMax string) string {
	if err := os.MkdirAll(capCgroup, 0o755); err != nil {
		return capCgroup + " could not be created"
	}
	cpuMaxPath := filepath.Join(capCgroup, "cpu.max")
	if err := os.WriteFile(cpuMaxPath, []byte(cpuMax+"\n"), 0o644); err != nil {
		return cpuMaxPath + " could not be written"
	}
	applied, _ := os.ReadFile(cpuMaxPath)
	if got := strings.TrimRight(string(applied), "\n"); got != cpuMax {
		return fmt.Sprintf("%s reads back '%s', not '%s'", cpuMaxPath, got, cpuMax)
	}
	return ""
}

func reportUncappedBuilds(ctx core.Context, reason string) {
	ctx.Info("erun-dind: WARNING: build containers are not CPU-capped: " + reason)
}
