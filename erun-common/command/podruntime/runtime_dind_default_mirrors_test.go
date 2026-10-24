package podruntime

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// DefaultRuntimeDindCPU is written down in two places that cannot read each
// other: this package's own rule and the erun-devops chart's fallback for
// runtime.dind.resources.limits.cpu (which an operator's own values file
// overrides, but which is what a deploy that sets nothing gets). The chart
// template says so in its own comment -- "keep the two in sync". This reads
// the real file rather than a copy: an edit to either that leaves the other
// behind fails here.
const (
	chartServicePath  = "erun-devops/k8s/erun-devops/templates/service.yaml"
	dindCPUFallbackRe = `(?m)^\{\{-\s*\$dindCPU\s*:=\s*default\s+"([^"]+)"\s*\$dindLimits\.cpu\s*-\}\}`
)

func TestDindCPUDefaultMatchesEveryMirror(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	for _, mirror := range []struct {
		name    string
		path    string
		pattern string
		what    string
	}{
		{
			name:    "the chart's own fallback",
			path:    chartServicePath,
			pattern: dindCPUFallbackRe,
			what:    "the erun-dind sidecar's CPU limit for a deploy that sets nothing",
		},
	} {
		t.Run(mirror.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, mirror.path))
			if err != nil {
				t.Fatalf("read %s: %v", mirror.path, err)
			}
			match := regexp.MustCompile(mirror.pattern).FindStringSubmatch(string(raw))
			if match == nil {
				t.Fatalf("%s no longer matches %s, so nothing establishes %s — a mirror nothing reads is "+
					"how the constant and the file it mirrors drift apart", mirror.path, mirror.pattern, mirror.what)
			}
			if match[1] != config.DefaultRuntimeDindCPU {
				t.Errorf("%s sets %s to %q, but DefaultRuntimeDindCPU is %q: raise or lower both together, since "+
					"they are one sizing decision written down twice", mirror.path, mirror.what, match[1], config.DefaultRuntimeDindCPU)
			}
		})
	}
}
