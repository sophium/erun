package execute

import (
	"errors"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/build/spec"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// A build that plans no work must not exit zero: callers read the exit code as a verdict.
func TestRunBuildExecutionRefusesAPlanThatDoesNothing(t *testing.T) {
	err := RunBuildExecution(core.Context{DryRun: true}, dockerspec.BuildExecutionSpec{}, nil, nil, nil, nil, cloud.CloudDependencies{})
	if err == nil {
		t.Fatal("expected a build that plans no work to fail instead of reporting success")
	}
	if !errors.Is(err, spec.ErrBuildPlanEmpty) {
		t.Fatalf("expected ErrBuildPlanEmpty, got %T: %v", err, err)
	}
}
