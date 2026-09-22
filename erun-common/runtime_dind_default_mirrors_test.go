package eruncommon

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// DefaultRuntimeDindCPU is written down in three places that cannot read each
// other: this package's own rule, the erun-devops chart's fallback for
// runtime.dind.resources.limits.cpu (which an operator's own values file
// overrides, but which is what a deploy that sets nothing gets), and the
// Dockerfile's DIND_CPU_LIMIT ARG default (which only a bare `docker build`
// with no erun environment resolved reaches). The chart template says so in
// its own comment -- "keep the two in sync" -- and nothing enforced it, which
// is how a value that is really one sizing decision would drift into three
// different ones. These read the real files rather than a copy: an edit to any
// of the three that leaves the others behind fails here.
const (
	chartServicePath  = "erun-devops/k8s/erun-devops/templates/service.yaml"
	devopsDockerfile  = "erun-devops/docker/erun-devops/Dockerfile"
	dindCPUFallbackRe = `(?m)^\{\{-\s*\$dindCPU\s*:=\s*default\s+"([^"]+)"\s*\$dindLimits\.cpu\s*-\}\}`
	dindCPUArgRe      = `(?m)^ARG\s+DIND_CPU_LIMIT=(\S+)\s*$`
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
		{
			name:    "the Dockerfile's build-arg default",
			path:    devopsDockerfile,
			pattern: dindCPUArgRe,
			what:    "the CPU budget an in-image gate sizes its widths against for a bare docker build",
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
			if match[1] != DefaultRuntimeDindCPU {
				t.Errorf("%s sets %s to %q, but DefaultRuntimeDindCPU is %q: raise or lower both together, since "+
					"they are one sizing decision written down three times", mirror.path, mirror.what, match[1], DefaultRuntimeDindCPU)
			}
		})
	}
}
