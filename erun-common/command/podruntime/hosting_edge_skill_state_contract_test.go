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
	hostingEdgeStatelessTable    = regexp.MustCompile(`(?s)\{("[^{}]*)\} as \$stateless\b`)
	hostingEdgeStatelessEntry    = regexp.MustCompile(`"([a-z0-9_.]+)(\[0\])?": "([^"]+)"`)
	hostingEdgePresenceCase      = regexp.MustCompile(`(?m)^\s*([a-z0-9_]+)\)\s+found=\$\(`)
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

// hostingEdgeStatelessShape is what the skill created before it kept state: its root passed the
// module cloudflare_api_token, services_zone and acme_email and nothing else, so only these
// objects, under the module's default names, can belong to an edge it applied then.
var hostingEdgeStatelessShape = map[string]string{
	"kubernetes_namespace.cert_manager":         "cert-manager",
	"helm_release.cert_manager[0]":              "cert-manager/cert-manager",
	"helm_release.traefik[0]":                   "traefik/traefik",
	"kubernetes_secret.cloudflare_api_token[0]": "cert-manager/erun-cloudflare-cloudflare-token",
	"helm_release.issuer":                       "cert-manager/erun-cloudflare-issuer",
}

// hostingEdgeAdoptionTable returns the address -> import id table the flow adopts from.
func hostingEdgeAdoptionTable(t *testing.T, flow string) map[string]string {
	t.Helper()
	match := hostingEdgeStatelessTable.FindStringSubmatch(flow)
	if match == nil {
		t.Fatal("the Apply flow adopts from no `$stateless` table: it imports whatever same-named object the cluster holds, an operator's own included, instead of only what the stateless skill could have created")
	}
	table := map[string]string{}
	for _, entry := range hostingEdgeStatelessEntry.FindAllStringSubmatch(match[1], -1) {
		table[entry[1]+entry[2]] = entry[3]
	}
	return table
}

// hostingEdgeCheckAdopted reports an adopted address the module does not declare as adopted, or
// whose presence the flow never checks, and returns its resource type.
func hostingEdgeCheckAdopted(t *testing.T, address string, resources, handled map[string]bool) string {
	t.Helper()
	base := strings.TrimSuffix(address, "[0]")
	if counted, declared := resources[base]; !declared {
		t.Errorf("adoption imports %s, which the module does not declare", address)
	} else if counted != strings.HasSuffix(address, "[0]") {
		t.Errorf("adoption addresses %s, but the module declares it with count=%t", address, counted)
	}
	kind, _, _ := strings.Cut(base, ".")
	if !handled[kind] {
		t.Errorf("adoption imports a %s but never checks the cluster holds one before importing it", kind)
	}
	return kind
}

func TestHostingEdgeSkillAdoptsOnlyWhatTheStatelessSkillCreated(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	table := hostingEdgeAdoptionTable(t, flow)
	for address, id := range hostingEdgeStatelessShape {
		if table[address] != id {
			t.Errorf("adoption imports %s as %q, want %q: the stateless skill created it under the module's default names", address, table[address], id)
		}
	}
	resources, handled := hostingEdgeModuleResources(t), map[string]bool{}
	for _, match := range hostingEdgePresenceCase.FindAllStringSubmatch(flow, -1) {
		handled[match[1]] = true
	}
	adopted := map[string]bool{}
	for address := range table {
		if _, ok := hostingEdgeStatelessShape[address]; !ok {
			t.Errorf("adoption imports %s, which the stateless skill never created: a same-named object there is someone else's", address)
		}
		adopted[hostingEdgeCheckAdopted(t, address, resources, handled)] = true
	}
	for kind := range hostingEdgeGateExempt(flow) {
		if !hostingEdgeModuleDeclaresType(resources, kind) {
			t.Errorf("the in-place gate exempts %s, which the module no longer declares", kind)
		}
		if adopted[kind] {
			t.Errorf("%s is both imported and exempted from the gate; an importable object's create means adoption is incomplete", kind)
		}
	}
}

func hostingEdgeModuleDeclaresType(resources map[string]bool, kind string) bool {
	for address := range resources {
		if strings.HasPrefix(address, kind+".") {
			return true
		}
	}
	return false
}

// hostingEdgeGateExempt returns the resource types the flow's gate exempts from its create check.
func hostingEdgeGateExempt(flow string) map[string]bool {
	exempt := map[string]bool{}
	if match := hostingEdgeGateExemptions.FindStringSubmatch(flow); match != nil {
		for _, name := range hostingEdgeQuoted.FindAllStringSubmatch(match[1], -1) {
			exempt[name[1]] = true
		}
	}
	return exempt
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
		{`held=$(terraform state list) || exit 1`, "read what the kept state holds, failing closed"},
		{`grep -qx 'helm_release.issuer'`, "take an issuer release the kept state holds as the edge, before probing any release by name"},
		{`helm list -q -n "${id%%/*}" --filter "^${id#*/}\$") || exit 1`, "only then look for an issuer release deployed without kept state"},
		{`"$HOME"/.erun/terraform/*/*/terraform.tfstate`, "refuse an edge a tenant tree's state owns, before importing anything"},
		{`sort_by(.[0] == "helm_release.issuer")`, "import the issuer release last, so an interrupted adoption resumes"},
		{`[ "${EDGE_ADOPT:-}" = confirmed ]`, "wait for the operator's confirmation before importing"},
		{`terraform import -input=false "$@" "$address" "$id" </dev/null || exit 1`, "import what the cluster holds and the state does not, stopping on a failed import"},
		{`terraform plan -input=false -out=edge.tfplan "$@" || exit 1`, "plan again after adopting"},
		{`index("delete")`, "refuse a destroy on any run, and a create on an existing edge"},
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
