package cmd

import (
	"bytes"
	"reflect"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func TestNewOpenShellRunnerWaitsBeforeExec(t *testing.T) {
	steps := []string{}
	req := common.ShellLaunchParams{Tenant: "tenant-a", Namespace: "tenant-a-dev"}
	run := newOpenShellRunner(
		func(got common.ShellLaunchParams) error {
			if !reflect.DeepEqual(got, req) {
				t.Fatalf("unexpected wait request: %+v", got)
			}
			steps = append(steps, "wait")
			return nil
		},
		func(got common.ShellLaunchParams) error {
			if !reflect.DeepEqual(got, req) {
				t.Fatalf("unexpected exec request: %+v", got)
			}
			steps = append(steps, "exec")
			return nil
		},
	)

	err := run(common.Context{Stderr: new(bytes.Buffer)}, req)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !reflect.DeepEqual(steps, []string{"wait", "exec"}) {
		t.Fatalf("unexpected call order: %#v", steps)
	}
}

func TestNewOpenShellRunnerSkipsExecOnWaitError(t *testing.T) {
	waitErr := common.ErrShellReattachDeploy
	run := newOpenShellRunner(
		func(common.ShellLaunchParams) error {
			return waitErr
		},
		func(common.ShellLaunchParams) error {
			t.Fatal("did not expect exec after wait failure")
			return nil
		},
	)

	err := run(common.Context{Stderr: new(bytes.Buffer)}, common.ShellLaunchParams{})
	if err != waitErr {
		t.Fatalf("expected %v, got %v", waitErr, err)
	}
}
