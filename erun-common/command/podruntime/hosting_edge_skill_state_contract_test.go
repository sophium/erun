package podruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

const (
	hostingEdgeSkillPath  = "erun-skills/skills/erun-enable-hosting-edge/SKILL.md"
	hostingEdgeModulePath = "erun-devops/terraform-erun/modules/terraform-erun-cluster-edge/main.tf"
	hostingEdgeRootFile   = "cat > erun-edge.tf <<'EOF'\n"
	// The init after the root file is written; the -from-module init before it runs with -backend=false.
	hostingEdgeBackendInit = "terraform init -input=false\n"
	hostingEdgeAppliedPlan = "terraform apply -input=false edge.tfplan"
	hostingEdgeKubeconfig  = "unset KUBE_CONFIG_PATH KUBE_IN_CLUSTER_CONFIG\n"
)

var (
	hostingEdgeKubernetesBackend = regexp.MustCompile(`(?s)\bbackend\s+"kubernetes"\s*\{\n(.*?)\n\s*\}`)
	hostingEdgeModuleResource    = regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)" "([a-z0-9_]+)" \{\n((?:.*\n)*?)\}\n`)
	hostingEdgeCounted           = regexp.MustCompile(`(?m)^  count\s*=`)
	hostingEdgeAdoptedType       = regexp.MustCompile(`\.type == "([a-z0-9_]+)"`)
	hostingEdgeGateExemptions    = regexp.MustCompile(`\.type \| IN\(([^)]*)\)`)
	hostingEdgeQuoted            = regexp.MustCompile(`"([^"]*)"`)
	hostingEdgePlanAddress       = regexp.MustCompile(`\.address == "([a-z0-9_.]+)(\[0\])?"`)
	hostingEdgeTerraformApply    = regexp.MustCompile(`(?m)^\s*terraform apply\b.*$`)
	hostingEdgeNumericSuffix     = regexp.MustCompile(`-[0-9]+$`)
)

// hostingEdgeApplyBlocks returns the Apply section's root-writing block and its plan-and-apply block.
func hostingEdgeApplyBlocks(t *testing.T) (setup, flow string) {
	t.Helper()
	_, apply, found := strings.Cut(readRepoText(t, hostingEdgeSkillPath), "\n## Apply\n")
	if !found {
		t.Fatal("the hosting-edge skill has no Apply section")
	}
	apply, _, _ = strings.Cut(apply, "\n## ")
	for _, block := range strings.Split(apply, "\n```sh\n")[1:] {
		block, _, _ = strings.Cut(block, "\n```\n")
		switch {
		case strings.Contains(block, "-from-module="):
			setup = block
		case strings.Contains(block, hostingEdgeAppliedPlan):
			flow = block
		}
	}
	if setup == "" || flow == "" {
		t.Fatal("the Apply section no longer has a block that writes the root with -from-module and one that applies the saved edge.tfplan")
	}
	return setup, flow
}

func hostingEdgeBackend(t *testing.T, setup string) map[string]string {
	t.Helper()
	_, root, found := strings.Cut(setup, hostingEdgeRootFile)
	if !found {
		t.Fatalf("the Apply flow no longer writes its root file with %q", strings.TrimSpace(hostingEdgeRootFile))
	}
	root, _, _ = strings.Cut(root, "\nEOF\n")
	match := hostingEdgeKubernetesBackend.FindStringSubmatch(root)
	if match == nil {
		t.Fatal("the root declares no kubernetes backend: every run starts from empty state, plans to create the existing edge again and fails with \"already exists\"")
	}
	attrs := map[string]string{}
	for _, line := range strings.Split(match[1], "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			attrs[strings.TrimSpace(name)] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return attrs
}

// hostingEdgeModuleResources maps each resource the module declares ("type.name") to whether it has a count.
func hostingEdgeModuleResources(t *testing.T) map[string]bool {
	t.Helper()
	resources := map[string]bool{}
	for _, match := range hostingEdgeModuleResource.FindAllStringSubmatch(readRepoText(t, hostingEdgeModulePath), -1) {
		resources[match[1]+"."+match[2]] = hostingEdgeCounted.MatchString(match[3])
	}
	if len(resources) == 0 {
		t.Fatalf("no resources parsed from %s", hostingEdgeModulePath)
	}
	return resources
}

func TestHostingEdgeSkillKeepsTheEdgeStateInTheCluster(t *testing.T) {
	setup, _ := hostingEdgeApplyBlocks(t)
	backend := hostingEdgeBackend(t, setup)
	if backend["namespace"] != "kube-system" {
		t.Errorf("the edge's state lives in namespace %q: it must be kube-system, which outlives the edge, not a namespace the module creates and its destroy deletes", backend["namespace"])
	}
	if suffix := backend["secret_suffix"]; suffix == "" || hostingEdgeNumericSuffix.MatchString(suffix) {
		t.Errorf("secret_suffix %q must be set and must not end in -<number>, which Terraform reserves for state chunks", suffix)
	}
	for _, pinned := range []string{"config_path", "config_paths", "in_cluster_config", "host", "token"} {
		if _, set := backend[pinned]; set {
			t.Errorf("the backend pins %s: it must find the cluster from the same kubeconfig as the providers and kubectl", pinned)
		}
	}
	if written, initialized := strings.Index(setup, hostingEdgeRootFile), strings.LastIndex(setup, hostingEdgeBackendInit); initialized < written {
		t.Errorf("the backend's %q must run after the root file is written", strings.TrimSpace(hostingEdgeBackendInit))
	}
}

// Terraform's providers and kubernetes backend read neither KUBECONFIG nor ~/.kube/config,
// so the flow must hand them the kubeconfig kubectl resolves — in a pod, the one its boot writes.
func TestHostingEdgeSkillHandsTerraformTheKubeconfigKubectlUses(t *testing.T) {
	setup, _ := hostingEdgeApplyBlocks(t)
	start := strings.Index(setup, hostingEdgeKubeconfig)
	if start < 0 {
		t.Fatalf("the Apply flow no longer starts its kubeconfig hand-off with %q", strings.TrimSpace(hostingEdgeKubeconfig))
	}
	snippet, _, found := strings.Cut(setup[start:], "\nfi\n")
	if !found {
		t.Fatal("the kubeconfig hand-off has no closing fi")
	}
	if strings.LastIndex(setup, hostingEdgeBackendInit) < start {
		t.Error("the kubeconfig hand-off must run before the backend's terraform init")
	}
	snippet += "\nfi\nprintf '%s|%s|%s' \"${KUBE_CONFIG_PATHS-unset}\" \"${KUBE_CONFIG_PATH-unset}\" \"${KUBE_IN_CLUSTER_CONFIG-unset}\"\n"

	pod := newTestRuntimeBoot(t, map[string]string{"KUBERNETES_SERVICE_HOST": "10.43.0.1"})
	writeServiceAccount(t, pod)
	if err := writeRuntimeKubeconfig(core.Context{}, pod, nil); err != nil {
		t.Fatal(err)
	}
	bare := t.TempDir()
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"an explicit KUBECONFIG list outranks the operator's KUBE_CONFIG_PATH", []string{"HOME=" + pod.home, "KUBECONFIG=/a/config:/b/config", "KUBE_CONFIG_PATH=/elsewhere", "KUBE_IN_CLUSTER_CONFIG=true"}, "/a/config:/b/config|unset|unset"},
		{"a pod without KUBECONFIG uses the kubeconfig its boot wrote", []string{"HOME=" + pod.home}, filepath.Join(pod.home, ".kube", "config") + "|unset|unset"},
		{"no kubeconfig at all leaves every client on the service account", []string{"HOME=" + bare}, "unset|unset|unset"},
	} {
		cmd := exec.Command("sh", "-c", snippet)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, tc.env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", tc.name, err, out)
		}
		if got := string(out); got != tc.want {
			t.Errorf("%s: KUBE_CONFIG_PATHS|KUBE_CONFIG_PATH|KUBE_IN_CLUSTER_CONFIG = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// hostingEdgeFlowTypes returns the resource types the flow's adoption imports and those its gate exempts.
func hostingEdgeFlowTypes(flow string) (adopted, exempt map[string]bool) {
	adopted, exempt = map[string]bool{}, map[string]bool{}
	for _, match := range hostingEdgeAdoptedType.FindAllStringSubmatch(flow, -1) {
		adopted[match[1]] = true
	}
	if match := hostingEdgeGateExemptions.FindStringSubmatch(flow); match != nil {
		for _, name := range hostingEdgeQuoted.FindAllStringSubmatch(match[1], -1) {
			exempt[name[1]] = true
		}
	}
	return adopted, exempt
}

func TestHostingEdgeSkillAdoptsEveryObjectTheModuleCreates(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	adopted, exempt := hostingEdgeFlowTypes(flow)
	declared := map[string]bool{}
	for address := range hostingEdgeModuleResources(t) {
		kind, _, _ := strings.Cut(address, ".")
		declared[kind] = true
		if !adopted[kind] && !exempt[kind] {
			t.Errorf("the module declares %s, which the Apply flow neither imports when adopting an edge nor exempts from its in-place gate: an edge applied before its state was kept would plan to create it again", address)
		}
	}
	for kind := range exempt {
		if !declared[kind] {
			t.Errorf("the in-place gate exempts %s, which the module no longer declares", kind)
		}
		if adopted[kind] {
			t.Errorf("%s is both imported and exempted from the gate; an importable object's create means adoption is incomplete", kind)
		}
	}
}

func TestHostingEdgeSkillReadsTheModuleAddressesFromThePlan(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	resources := hostingEdgeModuleResources(t)
	matches := hostingEdgePlanAddress.FindAllStringSubmatch(flow, -1)
	if len(matches) == 0 {
		t.Fatal("the Apply flow reads no resource address from the plan; it must find the issuer release and the Traefik chart there")
	}
	for _, match := range matches {
		counted, ok := resources[match[1]]
		if !ok {
			t.Errorf("the Apply flow reads %s%s from the plan, which the module does not declare", match[1], match[2])
		} else if counted != (match[2] != "") {
			t.Errorf("the Apply flow addresses %s%s, but the module declares it with count=%t", match[1], match[2], counted)
		}
	}
}

func TestHostingEdgeSkillAdoptsAndGatesBeforeItAppliesOnce(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	if applies := strings.Count(flow, hostingEdgeAppliedPlan); applies != 1 {
		t.Fatalf("the Apply flow applies %d times: it must apply once, after the gate and the CRD upgrade", applies)
	}
	at := 0
	for _, step := range []struct{ marker, why string }{
		{`terraform plan -input=false -out=edge.tfplan "$@" || exit 1`, "plan with the edge's inputs"},
		{`helm status "${release#*/}" -n "${release%%/*}"`, "detect an existing edge by its issuer release"},
		{`terraform import -input=false "$@" "$address" "$id" </dev/null`, "import what the cluster holds and the state does not"},
		{`if [ -n "$adopted" ]; then`, "plan again after adopting"},
		{`index("delete")`, "refuse a create or destroy on an existing edge"},
		{`helm show crds traefik`, "upgrade Traefik's CRDs before the chart"},
		{hostingEdgeAppliedPlan, "apply exactly the plan that was checked"},
	} {
		next := strings.Index(flow[at:], step.marker)
		if next < 0 {
			t.Fatalf("the Apply flow must %s (%q) after the previous step", step.why, step.marker)
		}
		at += next + len(step.marker)
	}
}

func TestHostingEdgeSkillAppliesOnlyTheReviewedPlan(t *testing.T) {
	skill := readRepoText(t, hostingEdgeSkillPath)
	if strings.Contains(skill, "-auto-approve") {
		t.Error("the skill still applies with -auto-approve, which bypasses the adoption and in-place gate")
	}
	for _, line := range hostingEdgeTerraformApply.FindAllString(skill, -1) {
		if strings.TrimSpace(line) != hostingEdgeAppliedPlan {
			t.Errorf("the skill runs %q: every apply must be %q, the plan the gate read", strings.TrimSpace(line), hostingEdgeAppliedPlan)
		}
	}
}

func TestSkillsSpecNamesTheHostingEdgeState(t *testing.T) {
	setup, _ := hostingEdgeApplyBlocks(t)
	secret := "tfstate-default-" + hostingEdgeBackend(t, setup)["secret_suffix"]
	spec := readRepoText(t, "erun-docs/docs/agent-reference/skills-spec.md")
	_, entry, found := strings.Cut(spec, "### `erun-enable-hosting-edge`\n")
	if !found {
		t.Fatal("skills-spec.md has no erun-enable-hosting-edge entry")
	}
	entry, _, _ = strings.Cut(entry, "\n### ")
	skill := readRepoText(t, hostingEdgeSkillPath)
	for _, name := range []string{secret, "lock-" + secret, "kube-system"} {
		if !strings.Contains(entry, "`"+name+"`") {
			t.Errorf("the skills-spec.md erun-enable-hosting-edge entry does not name `%s`, where the edge's state lives", name)
		}
		if !strings.Contains(skill, "`"+name+"`") {
			t.Errorf("the skill does not name `%s`, where the edge's state lives", name)
		}
	}
}
