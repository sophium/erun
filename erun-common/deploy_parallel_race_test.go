package eruncommon

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// parallelRaceTestSpecCount is deliberately > 1 so runDeployStep takes its
// real-parallel branch (len(specs) > 1 && !ctx.DryRun), and large enough that
// the fanned-out goroutines are very likely to be writing to the shared
// Context concurrently rather than by scheduling accident.
const parallelRaceTestSpecCount = 8

// parallelRaceTestSpecs builds specs whose KubernetesContext/Namespace are
// unique per component -- fake names not present in any kubeconfig, so the
// real (but fast-failing) kubectl invocation TraceEnsureKubernetesNamespace
// makes before it hits the stubbed ensureDeployNamespace resolves in
// milliseconds instead of touching a real cluster.
func parallelRaceTestSpecs() []DeploySpec {
	specs := make([]DeploySpec, parallelRaceTestSpecCount)
	for i := range specs {
		name := fmt.Sprintf("component-%d", i)
		specs[i] = DeploySpec{
			DeployContext: KubernetesDeployContext{ComponentName: name},
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

// TestRunDeployStepParallelSpecsShareNoWriter runs runDeployStep's real
// (non-dry-run) parallel branch against a Context shaped like the MCP edge's
// (erun-mcp/runtime.go builds Logger and Stdout/Stderr from plain
// bytes.Buffer values) and proves the fan-out never writes one buffer from
// more than one goroutine. Before the erun#1664 fix, every goroutine's
// RunDeploySpec call traced through the same Context, so ctx.Trace/ctx.Info
// raced on the shared buffers. ensureDeployNamespace is stubbed to fail fast
// so the test never reaches the real helm invocation or the on-disk
// single-flight marker.
func TestRunDeployStepParallelSpecsShareNoWriter(t *testing.T) {
	sentinel := errors.New("namespace-ensure-refused")
	original := ensureDeployNamespace
	ensureDeployNamespace = func(_, _ string) error { return sentinel }
	t.Cleanup(func() { ensureDeployNamespace = original })

	var stdout, stderr bytes.Buffer
	ctx := Context{
		Logger:    NewLoggerWithWriters(VerbosityTrace, &stdout, &stderr),
		Verbosity: VerbosityTrace,
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

// TestRunDeployStepParallelOutputAttributableToComponent proves the merged
// trace stays attributable per component instead of interleaving mid-line: each
// component's own kubectl trace lines (naming its own namespace) must appear as
// one contiguous block, in spec order, rather than mixed with another
// component's lines.
func TestRunDeployStepParallelOutputAttributableToComponent(t *testing.T) {
	sentinel := errors.New("namespace-ensure-refused")
	original := ensureDeployNamespace
	ensureDeployNamespace = func(_, _ string) error { return sentinel }
	t.Cleanup(func() { ensureDeployNamespace = original })

	var stdout, stderr bytes.Buffer
	ctx := Context{
		Logger:    NewLoggerWithWriters(VerbosityTrace, &stdout, &stderr),
		Verbosity: VerbosityTrace,
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
			// Only the immediate neighbourhood after this component's own
			// marker should be scanned for foreign markers -- bound it to
			// this component's own block by cutting at the next marker.
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
