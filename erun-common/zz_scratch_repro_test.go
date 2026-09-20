package eruncommon

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestScratchReproDescendantSetsid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix only")
	}
	isolateActivityCache(t)
	const tenant = "scratch-repro"
	const environment = "descendant-setsid"
	const id = "job"
	log := filepath.Join(t.TempDir(), "background.log")

	cmdline := fmt.Sprintf("setsid sleep 5 </dev/null >%s 2>&1 & exit 0", log)
	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Command:     []string{"bash", "-c", cmdline},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}
	t.Cleanup(func() { killProcessesMatching(log) })

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	out, _ := exec.Command("ps", "-axo", "pid=,ppid=,pgid=,sid=,stat=,cmd=").Output()
	var rows []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "sleep 5") {
			rows = append(rows, line)
		}
	}
	t.Logf("leftover rows: %v", rows)
	t.Logf("job child pid=%d state=%q succeeded=%v reason=%q", job.ChildPID, job.State, job.Succeeded, job.Reason)
	_ = strconv.Itoa
	_ = syscall.Getpid
	if job.Succeeded {
		t.Fatalf("REPRO: job reported success while a descendant-setsid background process is alive")
	}
}
