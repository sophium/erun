package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// Greater than 1 so runDeployStep takes its parallel branch.
const parallelRaceTestSpecCount = 8

// Fake contexts make the real kubectl call fail fast without a cluster.
func parallelRaceTestSpecs() []DeploySpec {
	specs := make([]DeploySpec, parallelRaceTestSpecCount)
	for i := range specs {
		name := fmt.Sprintf("component-%d", i)
		specs[i] = DeploySpec{
			DeployContext: shared.KubernetesDeployContext{ComponentName: name},
			Deploy: HelmDeploySpec{
				ReleaseName:       name,
				Namespace:         "ns-" + name,
				KubernetesContext: "ctx-" + name,
				ChartPath:         "oci://ghcr.io/sophium/charts/erun-" + name,
			},
		}
	}
	return specs
}

// Run with -race: the Context mirrors the MCP edge's plain bytes.Buffer writers.
func TestRunDeployStepParallelSpecsShareNoWriter(t *testing.T) {
	sentinel := errors.New("namespace-ensure-refused")
	original := ensureDeployNamespace
	ensureDeployNamespace = func(_, _ string) error { return sentinel }
	t.Cleanup(func() { ensureDeployNamespace = original })

	var stdout, stderr bytes.Buffer
	ctx := core.Context{
		Logger:    core.NewLoggerWithWriters(core.VerbosityTrace, &stdout, &stderr),
		Verbosity: core.VerbosityTrace,
		Stdout:    &stdout,
		Stderr:    &stderr,
	}

	err := runDeployStep(ctx, 0, parallelRaceTestSpecs(), func(HelmDeployParams) error {
		t.Error("the chart deployer must not run when the namespace ensure refuses")
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want every spec to fail with the namespace-ensure sentinel", err)
	}
}

func TestRunDeployStepParallelOutputAttributableToComponent(t *testing.T) {
	sentinel := errors.New("namespace-ensure-refused")
	original := ensureDeployNamespace
	ensureDeployNamespace = func(_, _ string) error { return sentinel }
	t.Cleanup(func() { ensureDeployNamespace = original })

	var stdout, stderr bytes.Buffer
	ctx := core.Context{
		Logger:    core.NewLoggerWithWriters(core.VerbosityTrace, &stdout, &stderr),
		Verbosity: core.VerbosityTrace,
		Stdout:    &stdout,
		Stderr:    &stderr,
	}

	specs := parallelRaceTestSpecs()
	err := runDeployStep(ctx, 0, specs, func(HelmDeployParams) error { return nil })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want every spec to fail with the namespace-ensure sentinel", err)
	}

	combined := stdout.String() + stderr.String()
	var lastEnd int
	for i, spec := range specs {
		marker := "namespace " + spec.Deploy.Namespace
		start := strings.Index(combined, marker)
		if start == -1 {
			t.Fatalf("component %d: marker %q not found in merged output:\n%s", i, marker, combined)
		}
		if start < lastEnd {
			t.Fatalf("component %d: marker %q appeared before the previous component's block (out of order merge)", i, marker)
		}
		block := combined[start:]
		for j, other := range specs {
			if j == i {
				continue
			}
			otherMarker := "namespace " + other.Deploy.Namespace
			end := len(block)
			if next := strings.Index(block[len(marker):], "namespace ns-"); next != -1 {
				end = len(marker) + next
			}
			if strings.Contains(block[:end], otherMarker) {
				t.Fatalf("component %d block contains component %d's marker %q -- output interleaved", i, j, otherMarker)
			}
		}
		lastEnd = start + len(marker)
	}
}
