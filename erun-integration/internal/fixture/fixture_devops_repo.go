package fixture

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
)

// WorkspaceSyncSSHStubSpec answers the several different questions one
// workspace-sync pass asks the same `ssh` — the pod's file listing, its
// fingerprints, and the archive its fetch step streams.
type WorkspaceSyncSSHStubSpec struct {
	// IndexPaths is what `git ls-files -coz` reports: the pod's Git-visible files.
	IndexPaths []string
	// StatLines are `stat -c '%s %Y %n'` records (size, mtime, path). They are
	// what lets a pass skip an unchanged file; with none, everything is fetched.
	StatLines []string
	// ArchivePath is a tar file the fetch step receives verbatim, standing in for
	// the pod's `tar -cf -`. Empty makes the fetch fail.
	ArchivePath string
}

// StubWorkspaceSyncSSH writes the argv-branching `ssh` stub a real sync pass
// needs and returns its env routing. The pass's fingerprint listing pipes the
// index listing into stat, so the stat branch has to be matched before the index
// listing it contains.
func StubWorkspaceSyncSSH(t testing.TB, dir string, spec WorkspaceSyncSSHStubSpec) []string {
	t.Helper()
	statBranch := ":"
	if len(spec.StatLines) > 0 {
		statBranch = `printf '%s\n' ` + shellSingleQuoteAll(spec.StatLines)
	}
	indexBranch := ":"
	if len(spec.IndexPaths) > 0 {
		indexBranch = `printf '%s\000' ` + shellSingleQuoteAll(spec.IndexPaths)
	}
	archiveBranch := "exit 1"
	if spec.ArchivePath != "" {
		archiveBranch = "cat " + shellSingleQuote(filepath.ToSlash(spec.ArchivePath))
	}
	// The readiness probe runs a bare `true`. The outputs listing's own script
	// exits 42 when its `cd` fails (erun-common's remoteOutputsDirAbsentExitCode)
	// -- the sentinel that tells "the outputs dir does not exist yet" apart from
	// every other listing failure, so "nothing there yet" must answer with that
	// exact code rather than the bare non-zero exit a real connection failure
	// would also produce.
	StubBinaryWithScript(t, dir, "ssh", `case "$*" in
  *' true') : ;;
  *'stat -c'*) `+statBranch+` ;;
  *'git ls-files -coz'*) `+indexBranch+` ;;
  *'git ls-files -dz'*) : ;;
  *'git ls-files -sz'*) : ;;
  *'find . -type f'*) exit 42 ;;
  *'tar --null'*) `+archiveBranch+` ;;
  *) exit 1 ;;
esac
exit 0
`)
	return StubEnv(dir, "ssh")
}

// TarEntry is one regular file in an archive written by WriteTarArchive.
type TarEntry struct {
	Path string
	Body []byte
}

// WriteTarArchive writes the archive a stubbed pod streams back. Entries carry
// an explicit mtime so a scenario can assert what the mirror did with the file
// it received rather than with whatever the host clock stamped.
func WriteTarArchive(t testing.TB, path string, entries []TarEntry, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for archive %s: %v", path, err)
	}
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.Path,
			Mode:     0o644,
			Size:     int64(len(entry.Body)),
			ModTime:  modTime,
			Typeflag: tar.TypeReg,
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write archive header %s: %v", entry.Path, err)
		}
		if _, err := writer.Write(entry.Body); err != nil {
			t.Fatalf("write archive body %s: %v", entry.Path, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close archive %s: %v", path, err)
	}
	mustWrite(t, path, buffer.String())
}

// RunGit runs git in dir so scenarios can set up branches, tags, or remotes
// after SeedReleaseRepo.
func RunGit(t testing.TB, dir string, args ...string) {
	t.Helper()
	if err := exec("git", args, dir); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// SeedDevopsRepo creates a minimal <tenant>-devops chart layout (including a
// per-environment values file) so deploy/build commands resolve a kubernetes
// deploy context.
func SeedDevopsRepo(t testing.TB, setup env.Setup, tenant, environment string) string {
	t.Helper()
	return SeedDevopsRepoAt(t, setup.Cwd, tenant, environment)
}

// SeedDevopsRepoAt is SeedDevopsRepo with an explicit project root, for
// scenarios whose tenant project root is not the default setup.Cwd (e.g. a
// git-initialized repo directory named after the tenant).
func SeedDevopsRepoAt(t testing.TB, root, tenant, environment string) string {
	t.Helper()
	devops := filepath.Join(root, tenant+"-devops")
	chart := filepath.Join(devops, "k8s", tenant+"-devops")
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chart, err)
	}
	mustWrite(t, filepath.Join(chart, "Chart.yaml"),
		"apiVersion: v2\nname: "+tenant+"-devops\nversion: 0.0.1\n",
	)
	mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(chart, "values."+strings.ToLower(strings.TrimSpace(environment))+".yaml"),
		"environment: "+environment+"\n",
	)
	mustWrite(t, filepath.Join(devops, "VERSION"), "1.0.0\n")
	return chart
}

// SeedDevopsRuntimeDockerfile writes the runtime-image Dockerfile so commands
// that resolve runtime-image builds (notably open --snapshot for the local
// environment) reach the docker-build branch in dry-run.
func SeedDevopsRuntimeDockerfile(t testing.TB, setup env.Setup, tenant string) {
	t.Helper()
	dir := filepath.Join(setup.Cwd, tenant+"-devops", "docker", tenant+"-devops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "Dockerfile"), "FROM alpine:3.22\n")
}

// SeedProjectDockerfile writes a minimal Dockerfile under setup.Cwd so
// commands that key off "current directory contains a Dockerfile" (notably
// the root `erun push` shorthand) register for the test invocation.
func SeedProjectDockerfile(t testing.TB, setup env.Setup) {
	t.Helper()
	mustWrite(t, filepath.Join(setup.Cwd, "Dockerfile"), "FROM alpine\n")
}

// SeedProjectK8sConfig writes the project .erun/config.yaml so deploy commands
// pick up the configured deployment plan (ordering + parallel grouping) instead
// of the hardcoded fallback.
func SeedProjectK8sConfig(t testing.TB, setup env.Setup, body string) {
	t.Helper()
	dir := filepath.Join(setup.Cwd, ".erun")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "config.yaml"), body)
}

// SeedGitignoredProjectConfig commits a ".erun/" gitignore rule (the exact
// shape seen in the wild: a blanket directory ignore that also excludes the
// per-project config it holds) and then writes .erun/config.yaml, so the
// config resolves from disk but git never tracks it. Requires a git repo
// already initialized at setup.Cwd (fixture.SeedGitRepo).
func SeedGitignoredProjectConfig(t testing.TB, setup env.Setup, body string) {
	t.Helper()
	mustWrite(t, filepath.Join(setup.Cwd, ".gitignore"), ".erun/\n")
	RunGit(t, setup.Cwd, "add", ".gitignore")
	RunGit(t, setup.Cwd, "commit", "-q", "-m", "ignore .erun")
	SeedProjectK8sConfig(t, setup, body)
}

// SeedTerraformEnvRoot materializes a platform's per-env Terraform root so erun
// terraform resolves a folder to run in, mirroring the layout the
// erun-blueprint-platform skill scaffolds. The per-env common.tf is a real
// symlink in production trees; the command only needs the folder + tfvars to
// resolve, so the fixture writes plain files.
func SeedTerraformEnvRoot(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	SeedTerraformEnvRootAt(t, filepath.Join(setup.Cwd, "terraform-"+tenant), environment)
}

// SeedTerraformEnvRootAt is SeedTerraformEnvRoot with an explicit base dir, for
// scenarios that relocate the Terraform base via .erun/config.yaml paths.terraform
// instead of using the conventional terraform-<tenant>/ root.
func SeedTerraformEnvRootAt(t testing.TB, root, environment string) {
	t.Helper()
	envDir := filepath.Join(root, environment)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", envDir, err)
	}
	mustWrite(t, filepath.Join(root, "common.tf"), "terraform {\n  required_version = \">= 1.3\"\n  backend \"local\" {}\n}\n")
	mustWrite(t, filepath.Join(root, "variables.tf"), "variable \"base_domain\" {\n  type = string\n}\n")
	mustWrite(t, filepath.Join(envDir, "main.tf"), "# "+environment+" services\n")
	mustWrite(t, filepath.Join(envDir, environment+".tfvars"), "base_domain = \"erunpaas.com\"\n")
}

// SeedProjectPathsConfig writes the project .erun/config.yaml paths: block so
// build/deploy/terraform resolve the docker/, k8s/, terraform, and VERSION
// locations from config instead of the <tenant>-devops convention. dockerContext
// sets paths.dockercontext (repo-root|component) to override the build-context
// heuristic. Empty fields are omitted.
func SeedProjectPathsConfig(t testing.TB, setup env.Setup, docker, dockerContext, k8s, terraform, version string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("paths:\n")
	for _, kv := range []struct{ key, value string }{
		{"docker", docker},
		{"dockercontext", dockerContext},
		{"k8s", k8s},
		{"terraform", terraform},
		{"version", version},
	} {
		if strings.TrimSpace(kv.value) != "" {
			b.WriteString("    " + kv.key + ": " + kv.value + "\n")
		}
	}
	SeedProjectK8sConfig(t, setup, b.String())
}

// ComponentPathsConfig is one .erun/config.yaml components:<Name> entry —
// the per-component twin of the fields SeedProjectPathsConfig writes under
// the project-global paths: block. Empty fields are omitted.
type ComponentPathsConfig struct {
	Name          string
	Docker        string
	DockerContext string
	K8s           string
	Terraform     string
	Version       string
}

// SeedProjectComponentsConfig writes the project .erun/config.yaml components:
// map so build/deploy resolve a per-component docker/k8s/version root for a
// monorepo of independent deployables that do not share one paths: root.
// Entries are written in the given order for deterministic golden output.
func SeedProjectComponentsConfig(t testing.TB, setup env.Setup, entries ...ComponentPathsConfig) {
	t.Helper()
	var b strings.Builder
	b.WriteString("components:\n")
	for _, entry := range entries {
		b.WriteString("    " + entry.Name + ":\n")
		for _, kv := range []struct{ key, value string }{
			{"docker", entry.Docker},
			{"dockercontext", entry.DockerContext},
			{"k8s", entry.K8s},
			{"terraform", entry.Terraform},
			{"version", entry.Version},
		} {
			if strings.TrimSpace(kv.value) != "" {
				b.WriteString("        " + kv.key + ": " + kv.value + "\n")
			}
		}
	}
	SeedProjectK8sConfig(t, setup, b.String())
}

// SeedDockerComponentAt writes <dockerDir>/<component>/Dockerfile so a build
// resolves a component build context at a caller-chosen docker root. dockerDir
// must be named "docker" — the standard-layout checks key off the folder name.
func SeedDockerComponentAt(t testing.TB, dockerDir, component string) {
	t.Helper()
	dir := filepath.Join(dockerDir, component)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "Dockerfile"), "FROM alpine:3.22\n")
}

// SeedK8sChartAt writes <k8sDir>/<chartName>/Chart.yaml (plus values files) so a
// deploy resolves a chart at a caller-chosen k8s root. k8sDir must be named
// "k8s" — the chart-discovery checks key off the folder name.
func SeedK8sChartAt(t testing.TB, k8sDir, chartName, tenant, environment string) {
	t.Helper()
	chart := filepath.Join(k8sDir, chartName)
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chart, err)
	}
	mustWrite(t, filepath.Join(chart, "Chart.yaml"), "apiVersion: v2\nname: "+chartName+"\nversion: 0.0.1\n")
	mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(chart, "values."+strings.ToLower(strings.TrimSpace(environment))+".yaml"), "environment: "+environment+"\n")
}

// SeedDevopsBackendCharts seeds the three opt-in backend charts
// (erun-backend-postgres, erun-backend-db, erun-backend-api) alongside
// SeedDevopsRepo's runtime chart so deploy --dry-run can resolve them.
func SeedDevopsBackendCharts(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	envSlug := strings.ToLower(strings.TrimSpace(environment))
	k8s := filepath.Join(setup.Cwd, tenant+"-devops", "k8s")
	for _, name := range []string{"erun-backend-postgres", "erun-backend-db", "erun-backend-api"} {
		chart := filepath.Join(k8s, name)
		if err := os.MkdirAll(chart, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", chart, err)
		}
		mustWrite(t, filepath.Join(chart, "Chart.yaml"),
			"apiVersion: v2\nname: "+name+"\nversion: 0.0.1\n",
		)
		mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
		mustWrite(t, filepath.Join(chart, "values."+envSlug+".yaml"), "environment: "+environment+"\n")
	}
}

// SeedDevopsComponentChart seeds a single non-opt-in, non-runtime component
// chart (e.g. a docs chart) to prove opt-in-only resolution does not auto-deploy
// it. Usable with or without SeedDevopsRepo: without it the tree is
// component-only and the runtime deploys via the published erun-devops chart.
func SeedDevopsComponentChart(t testing.TB, setup env.Setup, tenant, environment, chartName string) {
	t.Helper()
	envSlug := strings.ToLower(strings.TrimSpace(environment))
	chart := filepath.Join(setup.Cwd, tenant+"-devops", "k8s", chartName)
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chart, err)
	}
	mustWrite(t, filepath.Join(chart, "Chart.yaml"), "apiVersion: v2\nname: "+chartName+"\nversion: 0.0.1\n")
	mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(chart, "values."+envSlug+".yaml"), "environment: "+environment+"\n")
}

// SeedDevopsUmbrellaChart seeds a component chart with an OCI dependency on a
// published erun-* subchart (the erun-blueprint-platform pattern, e.g.
// team-backend-api wrapping erun-backend-api). deploy must helm dependency build
// such a chart before install, so use it to prove the dependency-build step is
// traced.
func SeedDevopsUmbrellaChart(t testing.TB, setup env.Setup, tenant, environment, chartName, dependencyName string) {
	t.Helper()
	envSlug := strings.ToLower(strings.TrimSpace(environment))
	chart := filepath.Join(setup.Cwd, tenant+"-devops", "k8s", chartName)
	if err := os.MkdirAll(chart, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", chart, err)
	}
	mustWrite(t, filepath.Join(chart, "Chart.yaml"),
		"apiVersion: v2\nname: "+chartName+"\nversion: 0.1.0\n"+
			"dependencies:\n"+
			"  - name: "+dependencyName+"\n"+
			"    version: \"1.0.0\"\n"+
			"    repository: \"oci://ghcr.io/sophium/charts\"\n",
	)
	mustWrite(t, filepath.Join(chart, "values.yaml"), "tenant: "+tenant+"\n")
	mustWrite(t, filepath.Join(chart, "values."+envSlug+".yaml"),
		dependencyName+":\n  tenant: "+tenant+"\n  environment: "+environment+"\n",
	)
}
