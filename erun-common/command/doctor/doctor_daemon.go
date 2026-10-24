package doctor

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/orchestration/diagnosis"
	"github.com/sophium/erun/erun-common/primitive/shared/diskheadroom"
)

// ResolveDoctorDockerDaemon picks the daemon from the environment's type.
func ResolveDoctorDockerDaemon(req config.ShellLaunchParams) model.DoctorDockerDaemon {
	switch {
	// Checked first: the chart's dind condition uses the same predicate.
	case req.Type.UsesDindSidecar():
		return model.DoctorDockerDaemon{
			Kind:      model.DoctorDockerDaemonDindSidecar,
			Container: model.RuntimeDindContainerName,
			Label:     "the " + model.RuntimeDindContainerName + " sidecar",
			Where: fmt.Sprintf("container %s of deployment/%s in namespace %s, where this environment's image builds run and where its build images are stored",
				model.RuntimeDindContainerName, config.RuntimeReleaseName(req.Tenant), req.Namespace),
		}
	case req.Type == config.EnvironmentTypeHost:
		return model.DoctorDockerDaemon{
			Kind:  model.DoctorDockerDaemonHost,
			Label: "this machine's docker daemon",
			Where: "the docker daemon on this machine, where a host environment's builds run (it is a worktree here with no pod)",
		}
	case req.Type == config.EnvironmentTypeRuntime:
		return model.DoctorDockerDaemon{
			Kind:  model.DoctorDockerDaemonNone,
			Label: "no build daemon",
			Where: "this environment resolves to type \"runtime\", which installs published versions by reference and never builds: its pod carries no " +
				model.RuntimeDindContainerName + " sidecar, so it holds no build images to prune. Prune a build environment (a local-agent, remote-agent, or host environment) instead",
		}
	default:
		return model.DoctorDockerDaemon{
			Kind:  model.DoctorDockerDaemonNone,
			Label: "no known build daemon",
			Where: fmt.Sprintf("this environment's type is %q, so doctor cannot tell which docker daemon holds its build images. Set the environment's type (local-agent, remote-agent, runtime, or host) and retry", string(req.Type)),
		}
	}
}

// DoctorDockerDaemonLine names the daemon a docker-storage section's figures came from.
func DoctorDockerDaemonLine(daemon model.DoctorDockerDaemon) string {
	return "docker storage: " + daemon.Report()
}

// DoctorActionDescriptionOn names a prune action and the daemon it acts on.
func DoctorActionDescriptionOn(action model.DoctorAction, daemon model.DoctorDockerDaemon) string {
	return fmt.Sprintf("%s (%s)", DoctorActionDescription(action), daemon.Label)
}

// doctorDockerStep is declared once so the pod transport (one script) and the
// host transport (direct exec) run the same steps.
type doctorDockerStep struct {
	header string
	// name defaults to the docker CLI.
	name string
	args []string
	// readKey marks output parsed as a store reading rather than printed.
	readKey string
	// tolerant steps may fail without failing the operation: the daemon root may
	// live inside dind or Docker Desktop's VM.
	tolerant bool
}

func (s doctorDockerStep) executable() string {
	if strings.TrimSpace(s.name) == "" {
		return "docker"
	}
	return s.name
}

// doctorStoreFormat puts the reclaimable figure last, so the parser also reads
// release_disk_headroom.go's two-field readings.
const doctorStoreFormat = "{{.Type}}|{{.Size}}|{{.Reclaimable}}"

// doctorDockerReadMarker delimits readings within one exec's output.
const doctorDockerReadMarker = "erun-doctor-read:"

func doctorInspectionSteps() []doctorDockerStep {
	return []doctorDockerStep{
		{header: "== Disk usage (/var/lib/docker) ==", name: "df", args: []string{"-h", "/var/lib/docker"}, tolerant: true},
		{header: "== Inode usage (/var/lib/docker) ==", name: "df", args: []string{"-i", "/var/lib/docker"}, tolerant: true},
		{header: "== Docker system df ==", args: []string{"system", "df"}},
	}
}

func doctorActionSteps(action model.DoctorAction) ([]doctorDockerStep, error) {
	var prune []string
	switch action {
	case model.DoctorActionPruneImages:
		prune = []string{"image", "prune", "-a", "-f"}
	case model.DoctorActionPruneBuildCache:
		prune = []string{"builder", "prune", "-a", "-f"}
	case model.DoctorActionPruneContainers:
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

func doctorDockerScript(steps []doctorDockerStep) string {
	lines := []string{"set -eu"}
	first := true
	for _, step := range steps {
		if step.header != "" {
			if !first {
				lines = append(lines, "printf '\\n'")
			}
			lines = append(lines, "printf '%s\\n' "+core.ShellQuote(step.header))
		}
		first = false
		command := step.executable() + " " + strings.Join(quotedDockerArgs(step.args), " ")
		if step.readKey == "" {
			lines = append(lines, command)
			continue
		}
		lines = append(lines,
			"printf '%s\\n' "+core.ShellQuote(doctorDockerReadMarker+step.readKey),
			command,
			"printf '%s\\n' "+core.ShellQuote(doctorDockerReadMarker+step.readKey+":end"),
		)
	}
	return strings.Join(lines, "\n")
}

// quotedDockerArgs quotes only what the shell would change, keeping traces readable.
func quotedDockerArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if shellSafeArg(arg) {
			quoted = append(quoted, arg)
			continue
		}
		quoted = append(quoted, core.ShellQuote(arg))
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

// DoctorDockerStore is a daemon's reclaimable bytes per store and in total,
// excluding local volumes, which no prune reaches.
type DoctorDockerStore struct {
	Reclaimable uint64
	Images      uint64
	BuildCache  uint64
}

// DoctorDockerResult is one operation's daemon, output and readings by readKey.
type DoctorDockerResult struct {
	Daemon   model.DoctorDockerDaemon
	Stdout   string
	Stderr   string
	Readings map[string]DoctorDockerStore
}

// DoctorPruneSummary reports what a prune freed, so a no-op prune cannot read
// as success; "" when readings are missing or it was a dry run.
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

// formatDockerStoreBytes uses docker's decimal units so it matches `docker system df`.
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

func runDoctorDockerSteps(ctx core.Context, runner seam.RuntimeContainerCommandRunnerFunc, req config.ShellLaunchParams, daemon model.DoctorDockerDaemon, label string, steps []doctorDockerStep) (DoctorDockerResult, error) {
	if daemon.Kind == model.DoctorDockerDaemonHost {
		return runDoctorDockerStepsLocally(ctx, daemon, steps)
	}
	script := doctorDockerScript(steps)
	preview := diagnosis.PreviewRuntimeContainerCommand(req, daemon.Container, script)
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
		runner = diagnosis.RunRuntimeContainerCommand
	}
	result, err := runner(req, daemon.Container, script)
	if err != nil {
		return DoctorDockerResult{Daemon: daemon, Stdout: result.Stdout, Stderr: result.Stderr},
			cloud.NormalizeDoctorKubectlError(req, result.Stderr, err)
	}
	stdout, readings := splitDoctorDockerReadings(result.Stdout)
	return DoctorDockerResult{Daemon: daemon, Stdout: stdout, Stderr: result.Stderr, Readings: readings}, nil
}

// runDoctorDockerStepsLocally runs each step directly, without a shell. A failed
// step does not abort the rest; it errors only when nothing succeeded.
func runDoctorDockerStepsLocally(ctx core.Context, daemon model.DoctorDockerDaemon, steps []doctorDockerStep) (DoctorDockerResult, error) {
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
			// Record only readings actually taken: a missing one must not read as zero.
			result.Readings[step.readKey] = store
		}
		failure.record(step, err)
	}
	result.Stdout = strings.TrimRight(stdout.String(), "\n")
	result.Stderr = strings.TrimRight(stderr.String(), "\n")
	if failure.err != nil {
		// Name the command: a host failure may be a missing CLI or a stopped daemon.
		return result, fmt.Errorf("`%s` failed against %s: %w", failure.command, daemon.Label, failure.err)
	}
	return result, nil
}

// doctorStepFailure keeps the first non-tolerant failure.
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
	cmd := core.Command(step.executable(), step.args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

func readLocalDockerStore(out []byte, err error) (DoctorDockerStore, bool) {
	if err != nil {
		return DoctorDockerStore{}, false
	}
	return parseDoctorDockerStore(string(out))
}

// splitDoctorDockerReadings keeps readings out of the printed report.
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

// parseDoctorDockerStore reads the reclaimable figure from the last field, so
// both the three- and two-field formats parse.
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
		reclaimable, ok := diskheadroom.ParseDockerSize(fields[len(fields)-1])
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
