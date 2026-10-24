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

// StubBinary writes a stub, routed via ERUN_<NAME>_BIN, that prints stdout and
// exits 0.
func StubBinary(t testing.TB, dir, name, stdout string) string {
	return StubBinaryAdvanced(t, dir, name, StubBinarySpec{Stdout: stdout})
}

// StubBinarySpec is a stub's fixed response, identical on every call.
type StubBinarySpec struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// StubBinaryAdvanced is StubBinary with explicit stderr and exit code.
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

// stubExecPath is the path erun execs for the stub: on Windows a compiled
// runner .exe, since Windows cannot exec a shebang script and .bat mangles argv.
func stubExecPath(dir, name string) string {
	path := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		return path + ".exe"
	}
	return path
}

// stubArgvPreamble rebuilds "$@" from the Windows runner's NUL-delimited argv
// file; it is inert when ERUN_STUB_ARGV_FILE is unset.
const stubArgvPreamble = "if [ -n \"${ERUN_STUB_ARGV_FILE:-}\" ]; then set --; while IFS= read -r -d '' __erun_arg; do set -- \"$@\" \"$__erun_arg\"; done < \"$ERUN_STUB_ARGV_FILE\"; fi\n"

// writeStub writes the script to dir/<name> and, on Windows, a runner .exe
// beside it.
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

// stubRunnerExe builds the Windows stub runner once per process.
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

// shellSingleQuoteAll renders values as space-separated printf operands.
func shellSingleQuoteAll(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, shellSingleQuote(value))
	}
	return strings.Join(quoted, " ")
}

// StubBinaryWithScript writes a stub whose shell body is scriptBody; the body
// should end with an explicit exit.
func StubBinaryWithScript(t testing.TB, dir, name, scriptBody string) string {
	t.Helper()
	body := "#!/bin/sh\n# erun integration stub for " + name + "\n" + scriptBody
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return writeStub(t, dir, name, body)
}

// StubKubectlGetJSON writes a kubectl stub answering `get <resource>` with
// responses[resource]; an unstubbed call exits 1 so a missing stub fails loudly.
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

// StubHelmObserve writes a helm stub answering `status` and `list`; any other
// subcommand exits 1.
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

// StubBinaryMergingIntoRemoteOnce writes a stub that, on its first call only,
// pushes a commit from repoDir to origin's branch, simulating a merge landing
// mid-command. It uses ERUN_GIT_BIN because the scrubbed PATH has no git.
func StubBinaryMergingIntoRemoteOnce(t testing.TB, dir, name, repoDir, branch, message string) string {
	t.Helper()
	// Forward slashes: Git Bash handles backslash paths unreliably.
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

// StubBinaryFailFirstThenSucceed writes a stub that fails once with
// stderrFirst and exitCode, then exits 0. The marker lives in dir.
func StubBinaryFailFirstThenSucceed(t testing.TB, dir, name, stderrFirst string, exitCode int) string {
	t.Helper()
	// Forward slashes: Git Bash handles backslash paths unreliably.
	marker := shellSingleQuote(filepath.ToSlash(filepath.Join(dir, name+"-failed-once")))
	script := "if [ ! -f " + marker + " ]; then\n" +
		"  : > " + marker + "\n" +
		"  printf '%s\\n' " + shellSingleQuote(stderrFirst) + " >&2\n" +
		"  exit " + strconv.Itoa(exitCode) + "\n" +
		"fi\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, name, script)
}

// StubKubectlRuntimeRunState writes a kubectl stub answering the runtime
// run-state jsonpath read with desired/ready; other calls exit 0.
func StubKubectlRuntimeRunState(t testing.TB, dir string, desired, ready int) string {
	t.Helper()
	script := "case \"$*\" in\n" +
		"  *jsonpath=*) printf '%s' " + shellSingleQuote(strconv.Itoa(desired)+"/"+strconv.Itoa(ready)) + "; exit 0 ;;\n" +
		"esac\n" +
		"exit 0"
	return StubBinaryWithScript(t, dir, "kubectl", script)
}

// KubectlWorktreeClaimStubSpec shapes the answer to `get pvc <claim> -o name`.
type KubectlWorktreeClaimStubSpec struct {
	ClaimName string
	// Stderr and ExitCode answer that read. NotFound with exit 1 means the
	// worktree is on the home volume; any other failure must read as unknown.
	Stderr   string
	ExitCode int
}

// StubKubectlWorktreeClaim writes a kubectl stub answering the worktree-claim
// read per spec; other calls exit 0.
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

// shellGlobEscape escapes case-pattern metacharacters in literal.
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

// StubKubectlScalingRuntime writes a kubectl stub that persists the replica
// count `kubectl scale` sets and answers later run-state reads with it, so
// ordering across erun invocations is observable.
func StubKubectlScalingRuntime(t testing.TB, dir, release string, desired int) string {
	t.Helper()
	// Forward slashes: Git Bash handles backslash paths unreliably.
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

// CodesignStubSpec configures the codesign stub.
type CodesignStubSpec struct {
	// AlreadySigned makes `codesign -d` report an existing signature.
	AlreadySigned bool
	// SignExitCode and SignStderr shape the signing call's answer.
	SignExitCode int
	SignStderr   string
}

// StubCodesign writes the codesign stub and returns its per-call argv log, the
// only way to prove a call did not happen.
func StubCodesign(t testing.TB, dir string, spec CodesignStubSpec) string {
	t.Helper()
	// Forward slashes: Git Bash handles backslash paths unreliably.
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

// PodExecStubSpec configures the pod emulator behind StubPodExec.
type PodExecStubSpec struct {
	// Root stands in for the pod's filesystem root.
	Root string
	// MaxStreamBytes is how much one exec may carry before failing with kubectl's
	// stream error; 0 means no limit.
	MaxStreamBytes int64
	// RawEncoding serves uncompressed ranges, like a pod without gzip.
	RawEncoding bool
	// FailOnceAt breaks the stream on the first read of each offset only.
	FailOnceAt []int64
	// FailAlwaysAt breaks the stream on every read of each offset.
	FailAlwaysAt []int64
}

// StubPodExec writes a kubectl stub that forwards to the podexec emulator.
func StubPodExec(t testing.TB, dir string, spec PodExecStubSpec) string {
	t.Helper()
	// Forward slashes: Git Bash handles backslash paths unreliably.
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

// PodExecBinary builds (once per process) the podexec emulator.
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

// StubEnv returns the ERUN_<NAME>_BIN pairs routing names to stubs in dir.
func StubEnv(dir string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		envName := "ERUN_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_BIN"
		out = append(out, envName+"="+stubExecPath(dir, name))
	}
	return out
}
