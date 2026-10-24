package podruntime

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

const dindTestPod = "erun-devops-7d9f-abcde"

type dindRun struct {
	root   string
	output bytes.Buffer
	argv   []string
	execed bool
}

// newDindRun builds an empty procfs/sysfs pair under a temp root and a host
// whose exec records the argv dockerd-entrypoint.sh would receive instead of
// starting a daemon.
func newDindRun(t *testing.T) *dindRun {
	t.Helper()
	run := &dindRun{root: t.TempDir()}
	mustMkdirAll(t, filepath.Join(run.root, "proc"))
	mustMkdirAll(t, filepath.Join(run.root, "sys"))
	return run
}

func (r *dindRun) host() dindEntrypointHost {
	return dindEntrypointHost{
		procRoot: filepath.Join(r.root, "proc"),
		sysRoot:  filepath.Join(r.root, "sys"),
		hostname: func() (string, error) { return dindTestPod, nil },
		lookPath: func(name string) (string, error) { return "/usr/local/bin/" + name, nil },
		exec: func(argv0 string, argv []string, _ []string) error {
			r.execed = true
			r.argv = argv[1:]
			if argv[0] != argv0 {
				return errors.New("argv[0] must be the program path")
			}
			return nil
		},
		environ: func() []string { return nil },
	}
}

func (r *dindRun) run(t *testing.T, dryRun bool, args ...string) {
	t.Helper()
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &r.output, &r.output), DryRun: dryRun}
	if err := runDindEntrypoint(ctx, DindEntrypointParams{DockerdArgs: args}, r.host()); err != nil {
		t.Fatalf("the entrypoint failed: %v\n%s", err, r.output.String())
	}
}

// stubNet writes a routing table whose default route is on iface, plus a
// non-default route on a decoy interface with a larger MTU, so a resolver that
// took the first row would be caught.
func (r *dindRun) stubNet(t *testing.T, iface, mtu string) {
	t.Helper()
	table := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"decoy0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
		iface + "\t00000000\t0128000A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	mustWriteFile(t, filepath.Join(r.root, "proc", "net", "route"), table)
	mustWriteFile(t, filepath.Join(r.root, "sys", "class", "net", iface, "mtu"), mtu+"\n")
	mustWriteFile(t, filepath.Join(r.root, "sys", "class", "net", "decoy0", "mtu"), "9000\n")
}

// stubOwnCgroup simulates the cgroup the kubelet already assigned this
// container, with the quota it enforces.
func (r *dindRun) stubOwnCgroup(t *testing.T, cpuMax string) {
	t.Helper()
	cgroupPath := "/kubepods.slice/cri-containerd-test.scope"
	mustWriteFile(t, filepath.Join(r.root, "proc", "self", "cgroup"), "0::"+cgroupPath+"\n")
	mustWriteFile(t, filepath.Join(r.root, "sys", "fs", "cgroup", filepath.FromSlash(cgroupPath), "cpu.max"), cpuMax+"\n")
}

// stubCapParent declares which controllers the kernel offers docker/ and which
// it already delegates to docker/'s children.
func (r *dindRun) stubCapParent(t *testing.T, controllers, subtree string) {
	t.Helper()
	mustWriteFile(t, filepath.Join(r.capParent(), "cgroup.controllers"), controllers+"\n")
	mustWriteFile(t, filepath.Join(r.capParent(), "cgroup.subtree_control"), subtree+"\n")
}

func (r *dindRun) capParent() string {
	return filepath.Join(r.root, "sys", "fs", "cgroup", "docker")
}

func (r *dindRun) capCgroup() string {
	return filepath.Join(r.capParent(), "erun-build-cpu-cap-"+dindTestPod)
}

func (r *dindRun) assertArgv(t *testing.T, want ...string) {
	t.Helper()
	if !r.execed {
		t.Fatalf("dockerd-entrypoint.sh was never handed over to:\n%s", r.output.String())
	}
	if len(want) == 0 && len(r.argv) == 0 {
		return
	}
	if !reflect.DeepEqual(r.argv, want) {
		t.Fatalf("dockerd args = %q, want %q", r.argv, want)
	}
}

// assertUncappedReported checks a run that could not apply the cap warned
// exactly once and never announced a cap it did not set: the silent no-op that
// reads as a working fix is the defect this coverage exists for.
func (r *dindRun) assertUncappedReported(t *testing.T, reason string) {
	t.Helper()
	out := r.output.String()
	if got := strings.Count(out, "WARNING: build containers are not CPU-capped"); got != 1 {
		t.Fatalf("expected exactly one uncapped warning, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, reason) {
		t.Fatalf("expected the warning to name %q:\n%s", reason, out)
	}
	if strings.Contains(out, "capping build containers") {
		t.Fatalf("a cap that was not applied must not be announced as applied:\n%s", out)
	}
}

func TestDindEntrypointPassesTheDefaultRouteMTU(t *testing.T) {
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.run(t, false)
	run.assertArgv(t, "--mtu=1450")
}

func TestDindEntrypointLeadsTheChartArgsWithTheMTU(t *testing.T) {
	// The derived MTU leads so the stock entrypoint still sees a leading '-'
	// and prepends dockerd.
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.run(t, false, "--insecure-registry", "10.1.2.3:5000")
	run.assertArgv(t, "--mtu=1450", "--insecure-registry", "10.1.2.3:5000")
}

func TestDindEntrypointHonoursJumboFrames(t *testing.T) {
	run := newDindRun(t)
	run.stubNet(t, "eth0", "8951")
	run.run(t, false)
	run.assertArgv(t, "--mtu=8951")
}

func TestDindEntrypointResolvesANonEth0Interface(t *testing.T) {
	run := newDindRun(t)
	run.stubNet(t, "ens5", "1400")
	run.run(t, false)
	run.assertArgv(t, "--mtu=1400")
}

func TestDindEntrypointWithoutADefaultRouteKeepsTheDaemonDefault(t *testing.T) {
	run := newDindRun(t)
	mustWriteFile(t, filepath.Join(run.root, "proc", "net", "route"), "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\n")
	run.run(t, false, "--insecure-registry", "10.1.2.3:5000")
	run.assertArgv(t, "--insecure-registry", "10.1.2.3:5000")
	if !strings.Contains(run.output.String(), "could not read the pod network MTU") {
		t.Fatalf("an unresolvable MTU should say so:\n%s", run.output.String())
	}
}

func TestDindEntrypointWithAnUnreadableProcfsAddsNoArgs(t *testing.T) {
	run := newDindRun(t)
	run.run(t, false)
	run.assertArgv(t)
}

func TestDindEntrypointRejectsImplausibleMTUs(t *testing.T) {
	for _, bogus := range []string{"", "not-a-number", "0", "68", "1279", "99999999999999999999"} {
		run := newDindRun(t)
		run.stubNet(t, "eth0", bogus)
		run.run(t, false)
		if len(run.argv) != 0 {
			t.Fatalf("an implausible MTU %q should be ignored, got %q", bogus, run.argv)
		}
	}
}

func TestDindEntrypointAcceptsExactlyTheIPv6Minimum(t *testing.T) {
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1280")
	run.run(t, false)
	run.assertArgv(t, "--mtu=1280")
}

func TestDindEntrypointMirrorsItsOwnQuotaIntoThePodCapCgroup(t *testing.T) {
	// cpu has to be delegated to the cap cgroup's parent first: until then
	// the child has no cpu.max to write at all.
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.stubOwnCgroup(t, "400000 100000")
	run.stubCapParent(t, "cpuset cpu io memory pids", "")
	run.run(t, false)
	if got := readTrimmed(t, filepath.Join(run.capParent(), "cgroup.subtree_control")); got != "+cpu" {
		t.Fatalf("expected cpu to be delegated via docker/cgroup.subtree_control, it holds %q", got)
	}
	if got := readTrimmed(t, filepath.Join(run.capCgroup(), "cpu.max")); got != "400000 100000" {
		t.Fatalf("cap cpu.max = %q, want the sidecar's own quota", got)
	}
	out := run.output.String()
	if !strings.Contains(out, "capping build containers via cgroup docker/erun-build-cpu-cap-"+dindTestPod+" (cpu.max 400000 100000)") {
		t.Fatalf("expected the applied cap to be announced:\n%s", out)
	}
	if strings.Contains(out, "WARNING") {
		t.Fatalf("an applied cap must not warn:\n%s", out)
	}
	run.assertArgv(t, "--mtu=1450")
}

func TestDindEntrypointMirrorsNothingForAnUnlimitedQuota(t *testing.T) {
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.stubOwnCgroup(t, "max 100000")
	run.run(t, false)
	if _, err := os.Stat(run.capCgroup()); err == nil {
		t.Fatal("expected no cap cgroup when this container's own cpu.max is unlimited")
	}
}

func TestDindEntrypointWithoutAReadableOwnCgroupCreatesNothing(t *testing.T) {
	run := newDindRun(t)
	run.run(t, false)
	if _, err := os.Stat(run.capParent()); err == nil {
		t.Fatal("expected no docker/ cgroup tree with no readable own cgroup")
	}
	run.assertArgv(t)
}

func TestDindEntrypointLeavesAnAlreadyDelegatedParentAlone(t *testing.T) {
	// The steady state from the second boot on.
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.stubOwnCgroup(t, "400000 100000")
	run.stubCapParent(t, "cpuset cpu io memory pids", "cpu")
	run.run(t, false)
	if got := readTrimmed(t, filepath.Join(run.capCgroup(), "cpu.max")); got != "400000 100000" {
		t.Fatalf("cap cpu.max = %q, want the sidecar's own quota", got)
	}
	if got := readTrimmed(t, filepath.Join(run.capParent(), "cgroup.subtree_control")); got != "cpu" {
		t.Fatalf("an already-delegating parent was rewritten: %q", got)
	}
}

func TestDindEntrypointReportsAParentThatDoesNotOfferCPU(t *testing.T) {
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.stubOwnCgroup(t, "400000 100000")
	run.stubCapParent(t, "cpuset io memory pids", "")
	run.run(t, false)
	run.assertUncappedReported(t, "does not list cpu")
	run.assertArgv(t, "--mtu=1450")
}

func TestDindEntrypointReportsARefusedDelegation(t *testing.T) {
	// The kernel's no-internal-process constraint surfaces as a failed write
	// here; the write has to be checked, not trusted.
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.stubOwnCgroup(t, "400000 100000")
	mustWriteFile(t, filepath.Join(run.capParent(), "cgroup.controllers"), "cpuset cpu io memory pids\n")
	mustMkdirAll(t, filepath.Join(run.capParent(), "cgroup.subtree_control"))
	run.run(t, false)
	run.assertUncappedReported(t, "cpu could not be delegated")
}

func TestDindEntrypointReportsACapCgroupThatCannotBeCreated(t *testing.T) {
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.stubOwnCgroup(t, "400000 100000")
	run.stubCapParent(t, "cpuset cpu io memory pids", "cpu")
	mustWriteFile(t, run.capCgroup(), "not a directory\n")
	run.run(t, false)
	run.assertUncappedReported(t, "could not be created")
}

func TestDindEntrypointDryRunReadsButNeitherWritesNorExecs(t *testing.T) {
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.stubOwnCgroup(t, "400000 100000")
	run.stubCapParent(t, "cpuset cpu io memory pids", "")
	run.run(t, true, "--insecure-registry", "10.1.2.3:5000")
	if run.execed {
		t.Fatal("a dry run must not hand over to dockerd-entrypoint.sh")
	}
	if _, err := os.Stat(run.capCgroup()); err == nil {
		t.Fatal("a dry run must not create the cap cgroup")
	}
	if got := readTrimmed(t, filepath.Join(run.capParent(), "cgroup.subtree_control")); got != "" {
		t.Fatalf("a dry run must not delegate cpu, subtree_control = %q", got)
	}
	out := run.output.String()
	for _, want := range []string{"MTU 1450", "would delegate cpu", "mirror cpu.max 400000 100000"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry run output lacks %q:\n%s", want, out)
		}
	}
}

func TestDindEntrypointRequiresTheStockEntrypointOnPath(t *testing.T) {
	run := newDindRun(t)
	host := run.host()
	host.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &run.output, &run.output)}
	err := runDindEntrypoint(ctx, DindEntrypointParams{}, host)
	if err == nil || !strings.Contains(err.Error(), "dockerd-entrypoint.sh is not on PATH") {
		t.Fatalf("expected a missing stock entrypoint to be named, got %v", err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}
