package podruntime

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The first distribution release patched for every advisory the registry image carries. The
// 2.x line ends at 2.8.3 with none of them patched, so registry:2 can never resolve past them.
var k3sSkillRegistryFloor = [3]int{3, 1, 2}

const k3sSkillRegistryManifestHeredoc = "cat > /var/lib/rancher/k3s/server/manifests/erun-registry.yaml <<'EOF'\n"

var k3sSkillRegistryImagePattern = regexp.MustCompile(`^(?:docker\.io/)?(?:library/)?registry:(\d+)\.(\d+)\.(\d+)$`)

// The 3.x image's bundled config adds a pprof and metrics listener on :5001, which on hostNetwork
// binds every node interface; it also allows deletes, logs at debug and drops nosniff.
var k3sSkillRegistryEnv = map[string]string{
	"REGISTRY_HTTP_ADDR":                           "0.0.0.0:5000",
	"REGISTRY_HTTP_DEBUG_ADDR":                     "",
	"REGISTRY_STORAGE_DELETE_ENABLED":              "false",
	"REGISTRY_LOG_LEVEL":                           "info",
	"REGISTRY_HTTP_HEADERS_X-CONTENT-TYPE-OPTIONS": "[nosniff]",
}

type k3sSkillRegistryDeployment struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Strategy struct {
			Type string `yaml:"type"`
		} `yaml:"strategy"`
		Template struct {
			Spec struct {
				HostNetwork bool `yaml:"hostNetwork"`
				Containers  []struct {
					Name  string `yaml:"name"`
					Image string `yaml:"image"`
					Env   []struct {
						Name  string `yaml:"name"`
						Value string `yaml:"value"`
					} `yaml:"env"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func readRepoText(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(erunRepoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// k3sSkillRegistryManifest returns the erun-registry Deployment exactly as Step 3 writes it.
func k3sSkillRegistryManifest(t *testing.T) k3sSkillRegistryDeployment {
	t.Helper()
	skill := readRepoText(t, "erun-skills/skills/erun-setup-k3s-cluster/SKILL.md")
	_, manifest, found := strings.Cut(skill, k3sSkillRegistryManifestHeredoc)
	if !found {
		t.Fatalf("the skill no longer writes its registry manifest with %q", strings.TrimSpace(k3sSkillRegistryManifestHeredoc))
	}
	manifest, _, found = strings.Cut(manifest, "\nEOF\n")
	if !found {
		t.Fatal("the skill's registry manifest heredoc has no closing EOF")
	}
	decoder := yaml.NewDecoder(bytes.NewBufferString(manifest))
	for {
		var doc k3sSkillRegistryDeployment
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode the skill's registry manifest: %v", err)
		}
		if doc.Kind == "Deployment" && doc.Metadata.Name == "erun-registry" {
			return doc
		}
	}
	t.Fatal("the skill's registry manifest has no erun-registry Deployment")
	return k3sSkillRegistryDeployment{}
}

func k3sSkillRegistryImage(t *testing.T, deployment k3sSkillRegistryDeployment) (string, map[string]string) {
	t.Helper()
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name != "registry" {
			continue
		}
		env := make(map[string]string, len(container.Env))
		for _, variable := range container.Env {
			env[variable.Name] = variable.Value
		}
		return container.Image, env
	}
	t.Fatal("the erun-registry Deployment has no registry container")
	return "", nil
}

func TestK3sClusterSkillRegistryIsPastTheDistributionAdvisories(t *testing.T) {
	image, _ := k3sSkillRegistryImage(t, k3sSkillRegistryManifest(t))
	match := k3sSkillRegistryImagePattern.FindStringSubmatch(image)
	if match == nil {
		t.Fatalf("the skill provisions %q: pin an exact registry:<major>.<minor>.<patch> release at or above %d.%d.%d; a floating tag drifts, and registry:2 resolves to 2.8.3, the last and unpatched 2.x", image, k3sSkillRegistryFloor[0], k3sSkillRegistryFloor[1], k3sSkillRegistryFloor[2])
	}
	var pinned [3]int
	for i, part := range match[1:] {
		pinned[i], _ = strconv.Atoi(part)
	}
	for i := range pinned {
		if pinned[i] == k3sSkillRegistryFloor[i] {
			continue
		}
		if pinned[i] < k3sSkillRegistryFloor[i] {
			t.Errorf("the skill provisions %s, below %d.%d.%d, the first distribution release patched for every advisory its image carries", image, k3sSkillRegistryFloor[0], k3sSkillRegistryFloor[1], k3sSkillRegistryFloor[2])
		}
		break
	}
}

func TestK3sClusterSkillRegistryKeepsTheTwoXBehaviour(t *testing.T) {
	deployment := k3sSkillRegistryManifest(t)
	_, env := k3sSkillRegistryImage(t, deployment)
	for name, want := range k3sSkillRegistryEnv {
		got, set := env[name]
		if !set {
			t.Errorf("the registry container does not set %s=%q", name, want)
		} else if got != want {
			t.Errorf("the registry container sets %s=%q, want %q", name, got, want)
		}
	}
	if !deployment.Spec.Template.Spec.HostNetwork {
		t.Error("the registry no longer runs on hostNetwork; the CNI hostPort path is unreliable on WSL2")
	}
	if deployment.Spec.Strategy.Type != "Recreate" {
		t.Errorf("the erun-registry Deployment uses strategy %q: a rolling update cannot start a second pod on the node's :5000, so an upgrade stalls; use Recreate", deployment.Spec.Strategy.Type)
	}
}

func TestSkillsSpecNamesTheK3sClusterSkillRegistry(t *testing.T) {
	image, _ := k3sSkillRegistryImage(t, k3sSkillRegistryManifest(t))
	spec := readRepoText(t, "erun-docs/docs/agent-reference/skills-spec.md")
	_, entry, found := strings.Cut(spec, "### `erun-setup-k3s-cluster`\n")
	if !found {
		t.Fatal("skills-spec.md has no erun-setup-k3s-cluster entry")
	}
	entry, _, _ = strings.Cut(entry, "\n### ")
	if !strings.Contains(entry, "`"+image+"`") {
		t.Errorf("the skills-spec.md erun-setup-k3s-cluster entry does not name the registry the skill provisions, `%s`", image)
	}
	if strings.Contains(entry, "`registry:2`") {
		t.Error("the skills-spec.md erun-setup-k3s-cluster entry still names `registry:2`")
	}
}
