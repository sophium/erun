package eruncommon

import (
	"bytes"
	"fmt"
	"strings"
)

// A doctor docker-storage read or prune acts on exactly one docker daemon, and
// which daemon that is decides whether the run means anything: an environment's
// build images live in the daemon its builds run in, so a prune of any other
// daemon reclaims nothing the build needs while still printing docker's own
// success. The daemon is therefore resolved from the environment rather than
// assumed from a container name, and every line the doctor prints about docker
// storage names the daemon it came from.

// DoctorDockerDaemonKind names the daemon a doctor docker-storage read or prune
// acts on.
type DoctorDockerDaemonKind string

const (
	// DoctorDockerDaemonDindSidecar is the erun-dind sidecar in the
	// environment's runtime pod: the daemon every image build in this
	// environment runs in, and so the one whose stores hold its build images.
	DoctorDockerDaemonDindSidecar DoctorDockerDaemonKind = "dind-sidecar"

	// DoctorDockerDaemonHost is the docker daemon on the machine the doctor
	// runs on. A host environment is a worktree on this machine with no pod,
	// so this is the daemon its builds run against.
	DoctorDockerDaemonHost DoctorDockerDaemonKind = "host"

	// DoctorDockerDaemonNone means no daemon holds this environment's build
	// images at all, with Where saying why.
	DoctorDockerDaemonNone DoctorDockerDaemonKind = "none"
)

// DoctorDockerDaemon is the daemon a doctor docker-storage read or prune acts
// on, resolved from the target environment.
type DoctorDockerDaemon struct {
	Kind DoctorDockerDaemonKind
	// Container is the pod container to exec into. Empty for a daemon this
	// process reaches directly (Kind host).
	Container string
	// Label names this daemon inside a report line, e.g. "the erun-dind sidecar".
	Label string
	// Where says which daemon this is and why it is the one that matters — the
	// container and deployment for a sidecar, this machine for a host daemon —
	// or, when Kind is none, why no daemon holds this environment's build
	// images. It is written to be read on its own, as the reason a requested
	// prune cannot run.
	Where string
}

// DoctorDockerDaemonUnavailableError reports that the requested docker-storage
// work cannot be done in this environment, with Daemon.Where as the reason. It
// is distinct from a daemon that could not be reached: nothing here is
// transient or broken, the daemon the operator asked for simply does not exist
// in this environment, and pruning some other daemon instead would report a
// reclaim this environment's builds cannot use.
type DoctorDockerDaemonUnavailableError struct {
	Daemon DoctorDockerDaemon
}

func (e DoctorDockerDaemonUnavailableError) Error() string {
	if strings.TrimSpace(e.Daemon.Where) == "" {
		return "no docker daemon in this environment holds its build images"
	}
	return e.Daemon.Where
}

// ResolveDoctorDockerDaemon resolves the daemon a doctor docker-storage read or
// prune must act on for this environment. The environment's own type decides it
// — the same signal the runtime chart uses to render the erun-dind sidecar at
// all (EnvironmentType.UsesDindSidecar) — because a prune aimed anywhere else
// reports a reclaim this environment's builds cannot use.
func ResolveDoctorDockerDaemon(req ShellLaunchParams) DoctorDockerDaemon {
	switch {
	// Checked before the type cases below: this is the predicate the chart's
	// own dind condition mirrors, so the two cannot disagree about which
	// environments have a sidecar.
	case req.Type.UsesDindSidecar():
		return DoctorDockerDaemon{
			Kind:      DoctorDockerDaemonDindSidecar,
			Container: runtimeDindContainerName,
			Label:     "the " + runtimeDindContainerName + " sidecar",
			Where: fmt.Sprintf("container %s of deployment/%s in namespace %s, where this environment's image builds run and where its build images are stored",
				runtimeDindContainerName, RuntimeReleaseName(req.Tenant), req.Namespace),
		}
	case req.Type == EnvironmentTypeHost:
		return DoctorDockerDaemon{
			Kind:  DoctorDockerDaemonHost,
			Label: "this machine's docker daemon",
			Where: "the docker daemon on this machine, where a host environment's builds run (it is a worktree here with no pod)",
		}
	case req.Type == EnvironmentTypeRuntime:
		return DoctorDockerDaemon{
			Kind:  DoctorDockerDaemonNone,
			Label: "no build daemon",
			Where: "this environment resolves to type \"runtime\", which installs published versions by reference and never builds: its pod carries no " +
				runtimeDindContainerName + " sidecar, so it holds no build images to prune. Prune a build environment (a local-agent, remote-agent, or host environment) instead",
		}
	default:
		return DoctorDockerDaemon{
			Kind:  DoctorDockerDaemonNone,
			Label: "no known build daemon",
			Where: fmt.Sprintf("this environment's type is %q, so doctor cannot tell which docker daemon holds its build images. Set the environment's type (local-agent, remote-agent, runtime, or host) and retry", string(req.Type)),
		}
	}
}

// Unavailable reports whether no daemon in this environment holds build images.
// A caller asked to prune must refuse with Where as the reason rather than act
// on some other daemon and report its success.
func (d DoctorDockerDaemon) Unavailable() bool {
	return d.Kind == DoctorDockerDaemonNone
}

// Report is the daemon's own one-line description: what it is, and where it is
// or why there is none.
func (d DoctorDockerDaemon) Report() string {
	return d.Label + " — " + d.Where
}

// DoctorDockerDaemonLine is the line that opens a docker-storage section,
// naming the daemon every figure below it came from: without it a table from an
// empty daemon reads exactly like one from the daemon the build just measured.
func DoctorDockerDaemonLine(daemon DoctorDockerDaemon) string {
	return "docker storage: " + daemon.Report()
}

// DoctorActionDescriptionOn names a prune action together with the daemon it
// will act on, for the line printed before it runs.
func DoctorActionDescriptionOn(action DoctorAction, daemon DoctorDockerDaemon) string {
	return fmt.Sprintf("%s (%s)", DoctorActionDescription(action), daemon.Label)
}

// doctorDockerStep is one step of a doctor docker-storage read or prune,
// declared once so both transports below run the same thing: the pod transport
// renders these into a single script for one kubectl exec, and the host
// transport runs each one directly. A step that means two different things on
// the two transports would be exactly the drift this type exists to prevent.
type doctorDockerStep struct {
	// header, when set, is printed before this step's output as a section title.
	header string
	// name is the executable to run, defaulting to the docker CLI.
	name string
	// args is the step's argv.
	args []string
	// readKey names a step whose output is a store reading the caller parses
	// rather than prints ("before"/"after" of a prune). Empty for a step whose
	// output is shown to the operator.
	readKey string
	// tolerant marks a step whose failure is reported but does not fail the
	// operation: reading the daemon's own root directory, which this process
	// cannot see whenever that root lives inside the daemon's filesystem (the
	// dind container, or a host daemon's VM on Docker Desktop). Every docker
	// step failing is the operation failing, wherever it ran.
	tolerant bool
}

// executable is the name this step's command runs as.
func (s doctorDockerStep) executable() string {
	if strings.TrimSpace(s.name) == "" {
		return "docker"
	}
	return s.name
}

// doctorStoreFormat is the docker system df format the machine-readable store
// readings are taken with: the reclaimed figure is the last field, so the same
// parser also reads the two-field readings release_disk_headroom.go takes.
const doctorStoreFormat = "{{.Type}}|{{.Size}}|{{.Reclaimable}}"

// doctorDockerReadMarker delimits a machine-readable reading inside one exec's
// output, so a pod transport can carry both readings and the human tables in a
// single round trip without the caller guessing where one ends.
const doctorDockerReadMarker = "erun-doctor-read:"

func doctorInspectionSteps() []doctorDockerStep {
	return []doctorDockerStep{
		{header: "== Disk usage (/var/lib/docker) ==", name: "df", args: []string{"-h", "/var/lib/docker"}, tolerant: true},
		{header: "== Inode usage (/var/lib/docker) ==", name: "df", args: []string{"-i", "/var/lib/docker"}, tolerant: true},
		{header: "== Docker system df ==", args: []string{"system", "df"}},
	}
}

func doctorActionSteps(action DoctorAction) ([]doctorDockerStep, error) {
	var prune []string
	switch action {
	case DoctorActionPruneImages:
		prune = []string{"image", "prune", "-a", "-f"}
	case DoctorActionPruneBuildCache:
		prune = []string{"builder", "prune", "-a", "-f"}
	case DoctorActionPruneContainers:
		prune = []string{"container", "prune", "-f"}
	default:
		return nil, fmt.Errorf("unsupported doctor action %q", action)
	}
	return []doctorDockerStep{
		{readKey: "before", args: []string{"system", "df", "--format", doctorStoreFormat}},
		{args: prune},
		{readKey: "after", args: []string{"system", "df", "--format", doctorStoreFormat}},
		{header: "== Docker system df ==", args: []string{"system", "df"}},
	}, nil
}

// doctorDockerScript renders steps as one shell script, which is what the pod
// transport hands to a single kubectl exec.
func doctorDockerScript(steps []doctorDockerStep) string {
	lines := []string{"set -eu"}
	first := true
	for _, step := range steps {
		if step.header != "" {
			// A blank line separates each section from the one above it, the
			// shape this report has always had.
			if !first {
				lines = append(lines, "printf '\\n'")
			}
			lines = append(lines, "printf '%s\\n' "+shellQuote(step.header))
		}
		first = false
		command := step.executable() + " " + strings.Join(quotedDockerArgs(step.args), " ")
		if step.readKey == "" {
			lines = append(lines, command)
			continue
		}
		lines = append(lines,
			"printf '%s\\n' "+shellQuote(doctorDockerReadMarker+step.readKey),
			command,
			"printf '%s\\n' "+shellQuote(doctorDockerReadMarker+step.readKey+":end"),
		)
	}
	return strings.Join(lines, "\n")
}

// quotedDockerArgs writes an argv as the script's own words, quoting only what
// the shell would otherwise change — so the traced script stays readable as the
// command it is, and every argument still arrives exactly as given.
func quotedDockerArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if shellSafeArg(arg) {
			quoted = append(quoted, arg)
			continue
		}
		quoted = append(quoted, shellQuote(arg))
	}
	return quoted
}

func shellSafeArg(arg string) bool {
	if arg == "" {
		return false
	}
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/', r == '=', r == ':', r == '@', r == ',', r == '+':
		default:
			return false
		}
	}
	return true
}

// DoctorDockerStore is what one docker daemon's own stores reported: the bytes
// it could still reclaim, per store and in total. Local volumes are excluded,
// the same as dockerReclaimableBytes, because no prune of docker's images or
// cache reaches them.
type DoctorDockerStore struct {
	Reclaimable uint64
	Images      uint64
	BuildCache  uint64
}

// DoctorDockerResult is one doctor docker-storage operation's outcome: the daemon
// it acted on, the output to show for it, and the store readings taken around
// it (keyed by each step's readKey).
type DoctorDockerResult struct {
	Daemon   DoctorDockerDaemon
	Stdout   string
	Stderr   string
	Readings map[string]DoctorDockerStore
}

// DoctorPruneSummary states what a prune did to the daemon it ran against, so a
// prune that freed nothing cannot pass as a success — the reported failure mode
// this wording exists for. It returns "" when there is nothing honest to say:
// a dry run, or readings that could not be taken.
func DoctorPruneSummary(result DoctorDockerResult) string {
	before, beforeOK := result.Readings["before"]
	after, afterOK := result.Readings["after"]
	if !beforeOK || !afterOK {
		return ""
	}
	label := result.Daemon.Label
	detail := func(store DoctorDockerStore) string {
		return fmt.Sprintf("images %s, build cache %s", formatDockerStoreBytes(store.Images), formatDockerStoreBytes(store.BuildCache))
	}
	if before.Reclaimable == 0 {
		return fmt.Sprintf("%s: nothing was reclaimable before the prune (%s), so nothing was freed here. "+
			"If this is not the daemon this environment's builds use, the space the build needs is elsewhere.",
			label, detail(before))
	}
	if after.Reclaimable >= before.Reclaimable {
		return fmt.Sprintf("%s: the prune freed nothing — the daemon still reports %s reclaimable after it (%s). "+
			"The space this environment's builds need was not reclaimed here.",
			label, formatDockerStoreBytes(after.Reclaimable), detail(after))
	}
	return fmt.Sprintf("%s: reclaimed %s of the %s the daemon reported reclaimable before the prune (now %s: %s).",
		label, formatDockerStoreBytes(before.Reclaimable-after.Reclaimable), formatDockerStoreBytes(before.Reclaimable),
		formatDockerStoreBytes(after.Reclaimable), detail(after))
}

// formatDockerStoreBytes renders a byte count the way the docker CLI renders the
// figures it was parsed from (decimal units), so a reported before/after
// compares directly with the `docker system df` table printed beside it.
func formatDockerStoreBytes(bytes uint64) string {
	switch {
	case bytes == 0:
		return "0B"
	case bytes >= 1e12:
		return fmt.Sprintf("%.2fTB", float64(bytes)/1e12)
	case bytes >= 1e9:
		return fmt.Sprintf("%.2fGB", float64(bytes)/1e9)
	case bytes >= 1e6:
		return fmt.Sprintf("%.2fMB", float64(bytes)/1e6)
	case bytes >= 1e3:
		return fmt.Sprintf("%.2fkB", float64(bytes)/1e3)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// runDoctorDockerSteps runs steps against daemon through the transport that
// daemon is reachable by, and returns both the output and the store readings.
func runDoctorDockerSteps(ctx Context, runner RuntimeContainerCommandRunnerFunc, req ShellLaunchParams, daemon DoctorDockerDaemon, label string, steps []doctorDockerStep) (DoctorDockerResult, error) {
	if daemon.Kind == DoctorDockerDaemonHost {
		return runDoctorDockerStepsLocally(ctx, daemon, steps)
	}
	script := doctorDockerScript(steps)
	preview := PreviewRuntimeContainerCommand(req, daemon.Container, script)
	traceArgs := append([]string{}, preview.Args...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
	if ctx.DryRun {
		return DoctorDockerResult{Daemon: daemon}, nil
	}
	if runner == nil {
		runner = RunRuntimeContainerCommand
	}
	result, err := runner(req, daemon.Container, script)
	if err != nil {
		return DoctorDockerResult{Daemon: daemon, Stdout: result.Stdout, Stderr: result.Stderr},
			normalizeDoctorKubectlError(req, result.Stderr, err)
	}
	stdout, readings := splitDoctorDockerReadings(result.Stdout)
	return DoctorDockerResult{Daemon: daemon, Stdout: stdout, Stderr: result.Stderr, Readings: readings}, nil
}

// runDoctorDockerStepsLocally is the host daemon's transport: each step is one
// docker CLI invocation on this machine, with no shell between them, so a host
// environment's prune does not depend on a POSIX shell being installed.
//
// A step that fails does not abort the rest: `df` over the daemon's root, in
// particular, is expected to fail on a machine where that root lives inside the
// daemon's own VM (Docker Desktop), and that failure is reported rather than
// hiding the readings that did succeed. The error is returned only when the
// daemon produced nothing at all, which is the case where there is no reading
// to show the operator.
func runDoctorDockerStepsLocally(ctx Context, daemon DoctorDockerDaemon, steps []doctorDockerStep) (DoctorDockerResult, error) {
	result := DoctorDockerResult{Daemon: daemon, Readings: map[string]DoctorDockerStore{}}
	var stdout, stderr bytes.Buffer
	var failure doctorStepFailure
	first := true
	for _, step := range steps {
		ctx.TraceCommand("", step.executable(), step.args...)
		if ctx.DryRun {
			continue
		}
		if step.header != "" {
			if !first {
				stdout.WriteString("\n")
			}
			stdout.WriteString(step.header + "\n")
		}
		first = false
		out, errOut, err := runLocalDockerStep(step)
		stderr.Write(errOut)
		if step.readKey == "" {
			stdout.Write(out)
		} else if store, ok := readLocalDockerStore(out, err); ok {
			// Only a reading that was actually taken is recorded: an absent
			// one has to stay absent, or a failed read would read as a daemon
			// that reported nothing reclaimable.
			result.Readings[step.readKey] = store
		}
		failure.record(step, err)
	}
	result.Stdout = strings.TrimRight(stdout.String(), "\n")
	result.Stderr = strings.TrimRight(stderr.String(), "\n")
	if failure.err != nil {
		// Named down to the command, because a host daemon can fail for
		// reasons that have nothing to do with docker (a docker CLI that is
		// not installed, a daemon that is not running) and the operator has
		// to be able to tell which one this was.
		return result, fmt.Errorf("`%s` failed against %s: %w", failure.command, daemon.Label, failure.err)
	}
	return result, nil
}

// doctorStepFailure keeps the first step that failed the operation. Only
// tolerant steps may fail without failing it, so every other step's failure is
// this operation's failure.
type doctorStepFailure struct {
	command string
	err     error
}

func (f *doctorStepFailure) record(step doctorDockerStep, err error) {
	if err == nil || step.tolerant || f.err != nil {
		return
	}
	f.command = strings.Join(append([]string{step.executable()}, step.args...), " ")
	f.err = err
}

func runLocalDockerStep(step doctorDockerStep) ([]byte, []byte, error) {
	cmd := Command(step.executable(), step.args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

// readLocalDockerStore parses one machine-readable store reading, which a step
// that failed did not produce.
func readLocalDockerStore(out []byte, err error) (DoctorDockerStore, bool) {
	if err != nil {
		return DoctorDockerStore{}, false
	}
	return parseDoctorDockerStore(string(out))
}

// splitDoctorDockerReadings separates the marker-delimited store readings from
// the output meant to be shown, so the readings never appear as loose lines in
// the report.
func splitDoctorDockerReadings(stdout string) (string, map[string]DoctorDockerStore) {
	lines := strings.Split(stdout, "\n")
	kept := make([]string, 0, len(lines))
	readings := map[string]DoctorDockerStore{}
	for i := 0; i < len(lines); i++ {
		key, ok := doctorReadMarkerKey(lines[i])
		if !ok {
			kept = append(kept, lines[i])
			continue
		}
		body := make([]string, 0, len(lines))
		for i+1 < len(lines) {
			i++
			if end, ok := doctorReadMarkerKey(lines[i]); ok && end == key+":end" {
				break
			}
			body = append(body, lines[i])
		}
		if store, ok := parseDoctorDockerStore(strings.Join(body, "\n")); ok {
			readings[key] = store
		}
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n"), readings
}

func doctorReadMarkerKey(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, doctorDockerReadMarker) {
		return "", false
	}
	return strings.TrimPrefix(trimmed, doctorDockerReadMarker), true
}

// parseDoctorDockerStore reads one `docker system df --format` reading. The
// reclaimed figure is taken from the line's last field so this reads both the
// three-field format above and the two-field one release_disk_headroom.go uses.
func parseDoctorDockerStore(text string) (DoctorDockerStore, bool) {
	var store DoctorDockerStore
	recognized := false
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		fields := strings.Split(strings.TrimSpace(line), "|")
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if strings.EqualFold(name, "local volumes") {
			continue
		}
		reclaimable, ok := parseDockerSize(fields[len(fields)-1])
		if !ok {
			continue
		}
		recognized = true
		store.Reclaimable += reclaimable
		switch {
		case strings.EqualFold(name, "images"):
			store.Images = reclaimable
		case strings.EqualFold(name, "build cache"):
			store.BuildCache = reclaimable
		}
	}
	return store, recognized
}
