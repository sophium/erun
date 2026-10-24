package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// StubBinary writes a POSIX-shell stub (routed via ERUN_<NAME>_BIN) that prints
// stdout and exits 0, to drive non-dry-run code paths without the real
// aws/kubectl/helm/docker binaries on PATH.
//
// For dry-run scenarios that need the stub to produce decision-input output
// (e.g. kubectl reporting "deployment exists" vs "not found" so the open runner
// can pick its branch), prefer StubBinaryAdvanced, which also sets stderr and
// exit code.
func StubBinary(t testing.TB, dir, name, stdout string) string {
	return StubBinaryAdvanced(t, dir, name, StubBinarySpec{Stdout: stdout})
}

// StubBinarySpec configures a stub binary's response. The stub always
// matches its argv to the spec deterministically: same Stdout/Stderr/ExitCode
// regardless of how it is called. Use one stub per per-binary persona; if a
// scenario needs a binary to behave differently across calls, write multiple
// stubs at distinct paths.
type StubBinarySpec struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// StubBinaryAdvanced is StubBinary with explicit stderr and exit code. See
// StubBinary for the env-var routing contract.
func StubBinaryAdvanced(t testing.TB, dir, name string, spec StubBinarySpec) string {
	t.Helper()
	body := "#!/bin/sh\n" +
		"# erun integration stub for " + name + "\n"
	if spec.Stdout != "" {
		body += "printf '%s\\n' " + shellSingleQuote(spec.Stdout) + "\n"
	}
	if spec.Stderr != "" {
		body += "printf '%s\\n' " + shellSingleQuote(spec.Stderr) + " >&2\n"
	}
	body += "exit " + strconv.Itoa(spec.ExitCode) + "\n"
	return writeStub(t, dir, name, body)
}

// stubExecPath is the path erun should exec for the stub, and the path StubEnv
// routes ERUN_<NAME>_BIN to. On Windows it is a compiled runner `.exe` (Windows
// can't exec a shebang script by name, and a .bat launcher mangles complex
// argv); everywhere else it is the sh script itself.
func stubExecPath(dir, name string) string {
	path := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		return path + ".exe"
	}
	return path
}

// stubArgvPreamble rebuilds "$@" from the NUL-delimited file the Windows stub
// runner writes, so a stub that branches on "$*"/"$1" sees the exact argv erun
// passed — which a Windows command line cannot preserve through sh. Inert
// everywhere else: with ERUN_STUB_ARGV_FILE unset the script keeps its real argv.
const stubArgvPreamble = "if [ -n \"${ERUN_STUB_ARGV_FILE:-}\" ]; then set --; while IFS= read -r -d '' __erun_arg; do set -- \"$@\" \"$__erun_arg\"; done < \"$ERUN_STUB_ARGV_FILE\"; fi\n"

// writeStub writes the sh-script body (with the argv preamble injected after the
// shebang) to dir/<name> and returns the path erun should exec. On Windows it
// also drops a dir/<name>.exe copy of the stub runner, which forwards argv
// faithfully to the sh script — so a stub erun execs by name works even though
// Windows can't launch a shebang script and a .bat can't preserve complex argv.
func writeStub(t testing.TB, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[:i+1] + stubArgvPreamble + body[i+1:]
	}
	scriptPath := filepath.Join(dir, name)
	if err := os.WriteFile(scriptPath, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", scriptPath, err)
	}
	if runtime.GOOS != "windows" {
		return scriptPath
	}
	exePath := scriptPath + ".exe"
	runner, err := os.ReadFile(stubRunnerExe(t))
	if err != nil {
		t.Fatalf("read stub runner: %v", err)
	}
	if err := os.WriteFile(exePath, runner, 0o755); err != nil {
		t.Fatalf("write stub runner copy %s: %v", exePath, err)
	}
	return exePath
}

var (
	stubRunnerOnce sync.Once
	stubRunnerPath string
	errStubRunner  error
)

// stubRunnerExe builds the argv-faithful stub runner once per test process and
// returns its path. Only used on Windows.
func stubRunnerExe(t testing.TB) string {
	t.Helper()
	stubRunnerOnce.Do(func() {
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			errStubRunner = fmt.Errorf("cannot locate fixture source to build stub runner")
			return
		}
		moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
		outDir, err := os.MkdirTemp("", "erun-stub-runner")
		if err != nil {
			errStubRunner = err
			return
		}
		out := filepath.Join(outDir, "erun-stub-runner.exe")
		cmd := harnessexec.Command("go", "build", "-o", out, "./internal/fixture/stubrunner")
		cmd.Dir = moduleRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			errStubRunner = fmt.Errorf("build stub runner: %w\n%s", err, output)
			return
		}
		stubRunnerPath = out
	})
	if errStubRunner != nil {
		t.Fatalf("stub runner: %v", errStubRunner)
	}
	return stubRunnerPath
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// shellSingleQuoteAll renders values as space-separated printf operands, so one
// format string repeats over them.
func shellSingleQuoteAll(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, shellSingleQuote(value))
	}
	return strings.Join(quoted, " ")
}

// StubBinaryWithScript writes a stub binary whose POSIX-shell body is the
// caller-supplied script. Use this when a stub must branch on argv (e.g.,
// the AWS CLI returning JSON for sts get-caller-identity but exit 0 for
// configure set). Less ergonomic than StubBinary, so prefer that for
// scenarios where one fixed response is enough.
//
// The script body has full shell access; "$*" / "$1" / "$2" are the args
// the production code passed. The body should end with an explicit exit.
func StubBinaryWithScript(t testing.TB, dir, name, scriptBody string) string {
	t.Helper()
	body := "#!/bin/sh\n# erun integration stub for " + name + "\n" + scriptBody
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return writeStub(t, dir, name, body)
}

// StubKubectlGetJSON writes a kubectl stub that dispatches on the resource
// named right after "get" (e.g. "pods", "certificates.cert-manager.io",
// "secret my-secret") to return canned JSON, for a command like `observe`
// that issues several distinct `kubectl get <resource> -o json` calls in one
// run. Matching on "get <resource>" as a substring of the full argv (rather
// than parsing --context/--namespace positionally) keeps this reusable across
// scenarios with different context/namespace flags. A call whose resource
// isn't in responses exits 1 naming the unstubbed argv, so a scenario that
// forgets to stub a call fails loudly instead of asserting on empty data.
func StubKubectlGetJSON(t testing.TB, dir string, responses map[string]string) string {
	t.Helper()
	keys := make([]string, 0, len(responses))
	for key := range responses {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var body strings.Builder
	body.WriteString("args=\"$*\"\n")
	body.WriteString("case \"$args\" in\n")
	for _, key := range keys {
		body.WriteString("  *" + shellSingleQuote("get "+key) + "*)\n")
		body.WriteString("    cat <<'ERUN_STUB_KUBECTL_JSON'\n")
		body.WriteString(strings.TrimRight(responses[key], "\n"))
		body.WriteString("\nERUN_STUB_KUBECTL_JSON\n")
		body.WriteString("    ;;\n")
	}
	body.WriteString("  *) echo \"unstubbed kubectl call: $args\" >&2; exit 1 ;;\n")
	body.WriteString("esac\n")
	return StubBinaryWithScript(t, dir, "kubectl", body.String())
}

// StubHelmObserve writes a helm stub that dispatches on the subcommand
// (status or list) to return statusStdout or listStdout, for observe's two
// distinct real read-only helm calls in one run: `helm status -o json`, which
// carries no chart metadata at all, and `helm list -o json`, which is where
// observe actually reads chart/appVersion (see fetchObservedHelmRelease). A
// call to any other subcommand exits 1 naming the unstubbed argv.
func StubHelmObserve(t testing.TB, dir, statusStdout, listStdout string) string {
	t.Helper()
	body := "case \"$1\" in\n" +
		"  status)\n" +
		"    cat <<'ERUN_STUB_HELM_STATUS'\n" +
		strings.TrimRight(statusStdout, "\n") + "\n" +
		"ERUN_STUB_HELM_STATUS\n" +
		"    ;;\n" +
		"  list)\n" +
		"    cat <<'ERUN_STUB_HELM_LIST'\n" +
		strings.TrimRight(listStdout, "\n") + "\n" +
		"ERUN_STUB_HELM_LIST\n" +
		"    ;;\n" +
		"  *) echo \"unstubbed helm call: $*\" >&2; exit 1 ;;\n" +
		"esac\n"
	return StubBinaryWithScript(t, dir, "helm", body)
}

// StubBinaryMergingIntoRemoteOnce writes a stub for name that, on its first
// invocation only, commits and pushes one commit from repoDir onto origin's
// branch, then exits 0 like a plain stub. It stands in for a pull request
// merging while a long-running command is working, so a scenario can place the
// move at a chosen point in the command's own execution rather than before it
// starts. git is reached through the ERUN_GIT_BIN seam the scenario already
// routes: the scrubbed PATH holds no git for a stub to find.
func StubBinaryMergingIntoRemoteOnce(t testing.TB, dir, name, repoDir, branch, message string) string {
	t.Helper()
	// Forward slashes: embedded in the sh stub, where Git Bash handles a
	// backslash Windows path unreliably.
	marker := shellSingleQuote(filepath.ToSlash(filepath.Join(dir, name+"-merged-once")))
	repo := shellSingleQuote(filepath.ToSlash(repoDir))
	script := "if [ ! -f " + marker + " ]; then\n" +
		"  : > " + marker + "\n" +
		"  \"$ERUN_GIT_BIN\" -C " + repo + " commit -q --allow-empty -m " + shellSingleQuote(message) + " || exit 1\n" +
		"  \"$ERUN_GIT_BIN\" -C " + repo + " push -q origin " + shellSingleQuote(branch) + " || exit 1\n" +
		"fi\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, name, script)
}

// StubBinaryFailFirstThenSucceed writes a stub that fails on its first
// invocation — printing stderrFirst to stderr and exiting exitCode — then
// drops a marker file so every later invocation exits 0 silently. Use it to
// drive a production retry/recovery loop through its failure arm into the
// success path (e.g. helm aborting an upgrade once, then succeeding after the
// recovery deletes the conflicting object). The marker lives in dir, so it is
// scoped to the scenario's stub directory and reset per scenario by env.New.
func StubBinaryFailFirstThenSucceed(t testing.TB, dir, name, stderrFirst string, exitCode int) string {
	t.Helper()
	// Forward slashes: embedded in the sh stub, where Git Bash handles a
	// backslash Windows path unreliably.
	marker := shellSingleQuote(filepath.ToSlash(filepath.Join(dir, name+"-failed-once")))
	script := "if [ ! -f " + marker + " ]; then\n" +
		"  : > " + marker + "\n" +
		"  printf '%s\\n' " + shellSingleQuote(stderrFirst) + " >&2\n" +
		"  exit " + strconv.Itoa(exitCode) + "\n" +
		"fi\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, name, script)
}

// StubKubectlRuntimeRunState writes a kubectl stub that answers the runtime
// run-state read `erun stop` and `erun open` perform (`get deployment <release>
// -o jsonpath={.spec.replicas}/{.status.readyReplicas}`) with the supplied
// replica counts. That answer is a dry-run decision input: it is what decides
// whether the environment is stopped and therefore whether a scale is planned,
// and a trace alone cannot supply it. Every other kubectl call exits 0
// silently, which is all the surrounding dry-run scenarios need.
//
// Pass desired=0 for a stopped environment and desired=1 for a running one.
func StubKubectlRuntimeRunState(t testing.TB, dir string, desired, ready int) string {
	t.Helper()
	script := "case \"$*\" in\n" +
		"  *jsonpath=*) printf '%s' " + shellSingleQuote(strconv.Itoa(desired)+"/"+strconv.Itoa(ready)) + "; exit 0 ;;\n" +
		"esac\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, "kubectl", script)
}

// KubectlWorktreeClaimStubSpec shapes the answer a kubectl stub gives to the
// deploy's worktree-volume check (`get pvc <release>-worktree -o name`).
type KubectlWorktreeClaimStubSpec struct {
	// ClaimName is the PVC the check reads.
	ClaimName string
	// Stderr and ExitCode answer that read. A NotFound stderr with exit 1 is an
	// environment whose worktree still lives on the home volume; any other
	// non-zero answer is a cluster the check cannot read at all, which deploy
	// must treat as "unknown" rather than "settled".
	Stderr   string
	ExitCode int
}

// StubKubectlWorktreeClaim writes a kubectl stub that answers the worktree-claim
// read as the spec says and exits 0 silently for everything else, so the rest of
// a real-run rollout still completes. It branches on argv, which is why it lives
// here rather than inline in a scenario.
func StubKubectlWorktreeClaim(t testing.TB, dir string, spec KubectlWorktreeClaimStubSpec) []string {
	t.Helper()
	script := "case \"$*\" in\n" +
		"  *" + shellGlobEscape("get pvc "+spec.ClaimName+" -o name") + "*)\n"
	if spec.Stderr != "" {
		script += "    printf '%s\\n' " + shellSingleQuote(spec.Stderr) + " >&2\n"
	}
	script += "    exit " + strconv.Itoa(spec.ExitCode) + " ;;\n" +
		"esac\n" +
		"exit 0"
	StubBinaryWithScript(t, dir, "kubectl", script)
	return StubEnv(dir, "kubectl")
}

// shellGlobEscape neutralizes the glob metacharacters a case pattern would
// otherwise interpret, so a literal fragment matches as itself.
func shellGlobEscape(literal string) string {
	var escaped strings.Builder
	for _, r := range literal {
		switch r {
		case '*', '?', '[', ']', '\\', ' ':
			escaped.WriteRune('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}

// StubKubectlScalingRuntime writes a kubectl stub that remembers the replica
// count `kubectl scale` last set and answers every later run-state read with
// it. It is what lets one scenario run a whole stop → reconnect → open sequence
// as a sequence: with the fixed-answer stub above, a later `erun` invocation
// cannot see what an earlier one did, so an ordering regression (a reconnect
// scaling a stopped environment back up) would be invisible.
//
// The deployment-presence read (`-o name`) answers with the release so `open`
// gets past its "is the runtime deployed" check and reaches the wake decision
// this stub exists to drive.
func StubKubectlScalingRuntime(t testing.TB, dir, release string, desired int) string {
	t.Helper()
	// Forward slashes: embedded in the sh stub, where Git Bash handles a
	// backslash Windows path unreliably.
	state := shellSingleQuote(filepath.ToSlash(filepath.Join(dir, "kubectl-replicas")))
	script := "state=" + state + "\n" +
		"[ -f \"$state\" ] || printf '%s' " + shellSingleQuote(strconv.Itoa(desired)) + " > \"$state\"\n" +
		"case \"$*\" in\n" +
		"  *--replicas=*)\n" +
		"    for arg in \"$@\"; do\n" +
		"      case \"$arg\" in --replicas=*) printf '%s' \"${arg#--replicas=}\" > \"$state\" ;; esac\n" +
		"    done\n" +
		"    exit 0 ;;\n" +
		"  *jsonpath=*) replicas=$(cat \"$state\"); printf '%s/%s' \"$replicas\" \"$replicas\"; exit 0 ;;\n" +
		"  *-o\\ name*) printf 'deployment.apps/%s\\n' " + shellSingleQuote(release) + "; exit 0 ;;\n" +
		"esac\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, "kubectl", script)
}

// CodesignStubSpec configures the codesign stub. Host-side ad-hoc signing asks
// codesign two different questions — "is this already signed?" (`codesign -d`)
// and "sign it" (`codesign -s - -f`) — so the stub has to branch on argv, which
// is why it lives here rather than inline in a scenario.
type CodesignStubSpec struct {
	// AlreadySigned makes the display probe report an existing signature, which
	// is the branch where production must leave the artifact alone.
	AlreadySigned bool
	// SignExitCode and SignStderr shape the signing call's answer; a non-zero
	// exit is the diagnosable failure that must not lose the artifact.
	SignExitCode int
	SignStderr   string
}

// StubCodesign writes the codesign stub and returns the path to a log file that
// records one line per invocation ("<argv>"). The log is the only way a scenario
// can prove a call did *not* happen — that an already-signed artifact was never
// re-signed — because absence leaves no trace in the command's own output.
func StubCodesign(t testing.TB, dir string, spec CodesignStubSpec) string {
	t.Helper()
	// Forward slashes: embedded in the sh stub, where Git Bash handles a
	// backslash Windows path unreliably.
	logPath := filepath.Join(dir, "codesign-calls.log")
	quotedLog := shellSingleQuote(filepath.ToSlash(logPath))
	displayExit := 1
	if spec.AlreadySigned {
		displayExit = 0
	}
	script := "printf '%s\\n' \"$*\" >> " + quotedLog + "\n" +
		"case \"$1\" in\n" +
		"  -d) printf '%s\\n' 'erun stub codesign display' >&2; exit " + strconv.Itoa(displayExit) + " ;;\n" +
		"esac\n"
	if spec.SignStderr != "" {
		script += "printf '%s\\n' " + shellSingleQuote(spec.SignStderr) + " >&2\n"
	}
	script += "exit " + strconv.Itoa(spec.SignExitCode)
	StubBinaryWithScript(t, dir, "codesign", script)
	return logPath
}

// PodExecStubSpec configures the pod emulator that answers an outputs download.
type PodExecStubSpec struct {
	// Root is a directory that stands in for the pod's filesystem root, so a
	// scenario seeds its payload at Root + the pod path erun will ask for.
	Root string
	// MaxStreamBytes is how much one exec may carry before it dies with
	// kubectl's own stream error. This is the failure being regression-tested:
	// the reported break sits between 12 MB and 14 MB of payload, and a whole
	// large file in one exec is what exceeds it. 0 means no limit.
	MaxStreamBytes int64
	// RawEncoding drops the emulator to uncompressed ranges, standing in for a
	// pod with no gzip.
	RawEncoding bool
	// FailOnceAt breaks the stream on the first read of each byte offset and
	// serves it normally afterwards, so a scenario can prove a range is retried.
	FailOnceAt []int64
	// FailAlwaysAt breaks the stream on every read of each byte offset, so a
	// scenario can prove what the download reports when a range never arrives.
	FailAlwaysAt []int64
}

// StubPodExec writes a kubectl stub that answers `erun outputs download`
// through the pod emulator in internal/fixture/podexec. A fixed-stdout stub
// cannot serve this path — the download asks the pod a different question per
// call — and the emulator is also what enforces MaxStreamBytes, the exec-stream
// break the download has to survive.
func StubPodExec(t testing.TB, dir string, spec PodExecStubSpec) string {
	t.Helper()
	// Forward slashes: embedded in the sh stub, where Git Bash handles a
	// backslash Windows path unreliably.
	args := []string{
		shellSingleQuote(filepath.ToSlash(PodExecBinary(t))),
		"--root", shellSingleQuote(filepath.ToSlash(spec.Root)),
		"--max-stream-bytes", strconv.FormatInt(spec.MaxStreamBytes, 10),
	}
	if spec.RawEncoding {
		args = append(args, "--encoding", "raw")
	}
	if len(spec.FailOnceAt) > 0 {
		args = append(args, "--fail-once-at", shellSingleQuote(joinInt64s(spec.FailOnceAt)),
			"--state", shellSingleQuote(filepath.ToSlash(filepath.Join(dir, "podexec-state"))))
	}
	if len(spec.FailAlwaysAt) > 0 {
		args = append(args, "--fail-always-at", shellSingleQuote(joinInt64s(spec.FailAlwaysAt)))
	}
	args = append(args, "--", "\"$@\"")
	return StubBinaryWithScript(t, dir, "kubectl", "exec "+strings.Join(args, " ")+"\n")
}

func joinInt64s(values []int64) string {
	rendered := make([]string, 0, len(values))
	for _, value := range values {
		rendered = append(rendered, strconv.FormatInt(value, 10))
	}
	return strings.Join(rendered, ",")
}

var (
	podExecBuildOnce sync.Once
	podExecPath      string
	errPodExecBuild  error
)

// PodExecBinary builds (once per process) the pod emulator that StubPodExec's
// kubectl stub forwards to. Same shape as PortSimBinary: the stub body needs a
// real program, and the scenario PATH holds none of the POSIX utilities erun's
// remote scripts use.
func PodExecBinary(t testing.TB) string {
	t.Helper()
	podExecBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "erun-podexec-*")
		if err != nil {
			errPodExecBuild = fmt.Errorf("mkdir podexec cache: %w", err)
			return
		}
		out := filepath.Join(dir, "podexec")
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			errPodExecBuild = fmt.Errorf("resolve fixture package path")
			return
		}
		cmd := harnessexec.Command("go", "build", "-o", out, ".")
		cmd.Dir = filepath.Join(filepath.Dir(thisFile), "podexec")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if combined, err := cmd.CombinedOutput(); err != nil {
			errPodExecBuild = fmt.Errorf("build podexec: %w: %s", err, combined)
			return
		}
		podExecPath = out
	})
	if errPodExecBuild != nil {
		t.Fatalf("%v", errPodExecBuild)
	}
	return podExecPath
}

// StubEnv returns the env-var pairs that route the named binary lookups to
// the stub at dir/<name>. Pass each result through env.Setup.Env() concat.
func StubEnv(dir string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		envName := "ERUN_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_BIN"
		out = append(out, envName+"="+stubExecPath(dir, name))
	}
	return out
}
