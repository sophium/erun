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

// WorkspaceSyncSSHStubSpec answers the `ssh` calls of one workspace-sync pass.
type WorkspaceSyncSSHStubSpec struct {
	IndexPaths []string
	// StatLines are `stat -c '%s %Y %n'` records; with none, everything is fetched.
	StatLines []string
	// ArchivePath stands in for the pod's `tar -cf -`; empty makes the fetch fail.
	ArchivePath string
}

// StubWorkspaceSyncSSH writes the `ssh` stub; the stat branch must match before the index listing it pipes from.
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
	// Exit 42 (remoteOutputsDirAbsentExitCode) means "outputs dir absent", distinct from a connection failure.
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

type TarEntry struct {
	Path string
	Body []byte
}

// WriteTarArchive writes entries with an explicit mtime so scenarios do not depend on the host clock.
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

func RunGit(t testing.TB, dir string, args ...string) {
	t.Helper()
	if err := exec("git", args, dir); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// SeedDevopsRepo creates a minimal <tenant>-devops chart layout under setup.Cwd.
func SeedDevopsRepo(t testing.TB, setup env.Setup, tenant, environment string) string {
	t.Helper()
	return SeedDevopsRepoAt(t, setup.Cwd, tenant, environment)
}

// SeedDevopsRepoAt is SeedDevopsRepo with an explicit project root.
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

// SeedDevopsRuntimeDockerfile writes the runtime-image Dockerfile.
func SeedDevopsRuntimeDockerfile(t testing.TB, setup env.Setup, tenant string) {
	t.Helper()
	dir := filepath.Join(setup.Cwd, tenant+"-devops", "docker", tenant+"-devops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "Dockerfile"), "FROM alpine:3.22\n")
}

// SeedProjectDockerfile writes a minimal Dockerfile under setup.Cwd.
func SeedProjectDockerfile(t testing.TB, setup env.Setup) {
	t.Helper()
	mustWrite(t, filepath.Join(setup.Cwd, "Dockerfile"), "FROM alpine\n")
}

// SeedProjectK8sConfig writes the project .erun/config.yaml.
func SeedProjectK8sConfig(t testing.TB, setup env.Setup, body string) {
	t.Helper()
	dir := filepath.Join(setup.Cwd, ".erun")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "config.yaml"), body)
}

// SeedGitignoredProjectConfig commits a blanket ".erun/" ignore, then writes .erun/config.yaml.
// Requires a git repo at setup.Cwd.
func SeedGitignoredProjectConfig(t testing.TB, setup env.Setup, body string) {
	t.Helper()
	mustWrite(t, filepath.Join(setup.Cwd, ".gitignore"), ".erun/\n")
	RunGit(t, setup.Cwd, "add", ".gitignore")
	RunGit(t, setup.Cwd, "commit", "-q", "-m", "ignore .erun")
	SeedProjectK8sConfig(t, setup, body)
}

// SeedTerraformEnvRoot writes a per-env Terraform root; common.tf is a plain file, not production's symlink.
func SeedTerraformEnvRoot(t testing.TB, setup env.Setup, tenant, environment string) {
	t.Helper()
	SeedTerraformEnvRootAt(t, filepath.Join(setup.Cwd, "terraform-"+tenant), environment)
}

// SeedTerraformEnvRootAt is SeedTerraformEnvRoot with an explicit base dir.
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

// SeedProjectPathsConfig writes the .erun/config.yaml paths: block, omitting empty fields.
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

// ComponentPathsConfig is one components:<Name> entry; empty fields are omitted.
type ComponentPathsConfig struct {
	Name          string
	Docker        string
	DockerContext string
	K8s           string
	Terraform     string
	Version       string
}

// SeedProjectComponentsConfig writes the components: map in the given order.
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

// SeedDockerComponentAt writes <dockerDir>/<component>/Dockerfile; dockerDir must be named "docker".
func SeedDockerComponentAt(t testing.TB, dockerDir, component string) {
	t.Helper()
	dir := filepath.Join(dockerDir, component)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustWrite(t, filepath.Join(dir, "Dockerfile"), "FROM alpine:3.22\n")
}

// SeedK8sChartAt writes a chart under k8sDir, which must be named "k8s".
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

// SeedDevopsBackendCharts seeds the three opt-in erun-backend-* charts.
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

// SeedDevopsComponentChart seeds one non-opt-in component chart, with or without SeedDevopsRepo.
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

// SeedDevopsUmbrellaChart seeds a chart with an OCI dependency on a published erun-* subchart.
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
