package podruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

const (
	runtimeMonitorInterval  = 30 * time.Second
	runtimeAIClientQuitWait = 20 * time.Second
)

func describeRuntimeEnvironmentMonitor(boot runtimeBoot) string {
	if !boot.hasTarget() {
		return ""
	}
	description := "sample resident activity every " + runtimeMonitorInterval.String()
	if boot.flag("ERUN_CLOUD_ENVIRONMENT") {
		description += " and stop the cloud host once the idle policy fires"
	}
	return description
}

// startRuntimeEnvironmentMonitor runs the pod's one activity loop. Every tick
// samples the processes working in this container, so a build or agent run
// nobody instrumented still registers as activity; the sample is taken before
// the sleep, so the first tick sets the CPU baseline the next compares against.
// Auto-stop rides the same tick, only for a cloud-managed environment, the only
// kind with a host to stop.
func startRuntimeEnvironmentMonitor(_ core.Context, boot runtimeBoot, supervisor *runtimeSupervisor) error {
	dir := boot.path(".erun", boot.tenant(), boot.environment())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// idle-stop.log is on the home volume and survives restarts; it is cleared
	// here so the desktop never shows a stop error from an earlier pod.
	stopLog := filepath.Join(dir, "idle-stop.log")
	if err := os.WriteFile(stopLog, nil, 0o644); err != nil {
		return err
	}
	monitorLog := filepath.Join(dir, "idle-monitor.log")
	cloud := boot.flag("ERUN_CLOUD_ENVIRONMENT")
	supervisor.goLoop(func() bool {
		_ = supervisor.run(boot.erunCommand("activity", "sample", "--tenant", boot.tenant(), "--environment", boot.environment()))
		if !supervisor.sleep(runtimeMonitorInterval) {
			return false
		}
		if !cloud {
			return true
		}
		return !runtimeIdleStopTick(boot, supervisor, monitorLog, stopLog)
	})
	return nil
}

func (b runtimeBoot) erunCommand(args ...string) *exec.Cmd {
	return core.Command(b.erunPath, args...)
}

// runtimeIdleStopTick asks the idle policy whether to stop, records the answer,
// and stops the host when it fires. It reports whether the host was stopped,
// which ends the monitor.
func runtimeIdleStopTick(boot runtimeBoot, supervisor *runtimeSupervisor, monitorLog, stopLog string) bool {
	var check bytes.Buffer
	cmd := boot.erunCommand("activity", "stop-ready", "--json", "--tenant", boot.tenant(), "--environment", boot.environment())
	cmd.Stdout = &check
	exitCode := runtimeCommandExitCode(supervisor.run(cmd))
	checkJSON := strings.TrimSpace(check.String())
	if !json.Valid([]byte(checkJSON)) {
		checkJSON = "null"
	}
	appendRuntimeLog(monitorLog, fmt.Sprintf("{\"ts\":%q,\"exit\":%d,\"check\":%s}\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"), exitCode, checkJSON))
	if exitCode != 0 {
		return false
	}
	// Reset per attempt: empty on success, the latest error on failure.
	log, err := os.OpenFile(stopLog, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	defer func() { _ = log.Close() }()
	// The host is asked to stop first: stop-instances returns at once and the
	// shutdown follows, leaving time to quit clients gracefully, while quitting
	// them first would destroy their state for nothing when the stop is refused.
	// A refused or failed stop leaves everything running, and the next tick
	// retries; its error stays in idle-stop.log for the desktop until then.
	if err := stopRuntimeCloudHost(boot, supervisor, log); err != nil {
		_, _ = fmt.Fprintln(log, err)
		return false
	}
	// stop-ready's Fire cleared stop-pending.json, so the JSON it printed is the
	// only surviving record of why; the stop already happened, so this is
	// best-effort.
	record := boot.erunCommand("activity", "record-stop", "--tenant", boot.tenant(), "--environment", boot.environment(), "--source", "pod-monitor", "--state-stdin")
	record.Stdin = strings.NewReader(checkJSON + "\n")
	record.Stdout, record.Stderr = log, log
	_ = supervisor.run(record)
	quitRuntimeAIClients(boot.procRoot, log)
	return true
}

func runtimeCommandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func appendRuntimeLog(path, line string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = file.WriteString(line)
	_ = file.Close()
}

// stopRuntimeCloudHost stops the EC2 instance the environment runs on.
func stopRuntimeCloudHost(boot runtimeBoot, supervisor *runtimeSupervisor, log io.Writer) error {
	if _, err := exec.LookPath("aws"); err != nil {
		return errors.New("aws CLI is not installed; cannot stop cloud host")
	}
	region, instanceID := boot.get("ERUN_CLOUD_REGION"), boot.get("ERUN_CLOUD_INSTANCE_ID")
	if region == "" || instanceID == "" {
		return errors.New("cloud host region or instance id is not available; cannot stop cloud host")
	}
	cmd := core.Command("aws", "--cli-connect-timeout", "5", "--cli-read-timeout", "20", "ec2", "stop-instances", "--region", region, "--instance-ids", instanceID)
	cmd.Env = append(os.Environ(), "AWS_MAX_ATTEMPTS=5", "AWS_RETRY_MODE=standard")
	cmd.Stderr = log
	return supervisor.run(cmd)
}

// runtimeAIClientPackages are the npm packages the image installs the AI clients
// from. Claude Code runs as a native binary inside its package
// (bin/claude.exe, reached through the /usr/local/bin/claude symlink) and Codex's
// node launcher starts a native binary vendored inside its package, so a process
// whose executable lies in one of these is an AI client however it was named.
var runtimeAIClientPackages = []string{"/@anthropic-ai/claude-code/", "/@openai/codex/"}

var runtimeAIClientNames = map[string]bool{"claude": true, "codex": true}

// isRuntimeAIClient matches the AI clients by what actually runs: the executable
// path, the program name, or the script a node launcher runs.
func isRuntimeAIClient(exe string, argv []string) bool {
	for _, pkg := range runtimeAIClientPackages {
		if strings.Contains(exe, pkg) {
			return true
		}
	}
	for i, arg := range argv {
		if i > 1 {
			break
		}
		if i == 1 && !strings.HasPrefix(filepath.Base(argv[0]), "node") {
			break
		}
		if runtimeAIClientNames[filepath.Base(arg)] {
			return true
		}
		for _, pkg := range runtimeAIClientPackages {
			if strings.Contains(arg, pkg) {
				return true
			}
		}
	}
	return false
}

// runtimeAIClientPIDs lists the AI client processes under procRoot.
func runtimeAIClientPIDs(procRoot string) []int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		dir := filepath.Join(procRoot, entry.Name())
		cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue // a zombie has no command line and nothing left to quit
		}
		exe, _ := os.Readlink(filepath.Join(dir, "exe"))
		if isRuntimeAIClient(exe, strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// quitRuntimeAIClients asks every running Claude Code and Codex to quit before the
// host powers off, so their session state is written, waits up to 20s for them,
// and flushes filesystem buffers.
func quitRuntimeAIClients(procRoot string, log io.Writer) {
	pids := runtimeAIClientPIDs(procRoot)
	for _, pid := range pids {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Signal(syscall.SIGTERM)
		}
	}
	if len(pids) > 0 {
		_, _ = fmt.Fprintf(log, "erun: asked %d AI client process(es) to quit before the host stops\n", len(pids))
	}
	deadline := time.Now().Add(runtimeAIClientQuitWait)
	for time.Now().Before(deadline) && len(runtimeAIClientPIDs(procRoot)) > 0 {
		time.Sleep(time.Second)
	}
	syncRuntimeFilesystems()
}
