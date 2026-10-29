package podruntime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	// The hosting edge's tools: what the flow runs and the prerequisites must find.
	hostingEdgeStubbedTools = "terraform kubectl helm jq"
	hostingEdgeTreeState    = `"$HOME"/.erun/terraform/*/*/terraform.tfstate`
	hostingEdgeAdoptConfirm = `[ "${EDGE_ADOPT:-}" = confirmed ]`
)

var (
	hostingEdgeToolCall       = regexp.MustCompile(`(?m)(?:^\s*|\$\(|[|;&{]\s*|\b(?:if|then|do|else|while|until|!)\s+)(terraform|kubectl|helm|jq)\b`)
	hostingEdgeStopsOnFailure = regexp.MustCompile(`\|\|\s*(?:exit 1|\{[^{}]*\bexit 1;?\s*\})\s*(?:;;)?\s*$`)
	hostingEdgeShellKeyword   = regexp.MustCompile(`(?m)(?:^\s*|[;&|]\s*|\b(?:then|do|else)\s+)(if|fi|for|while|until|done|case|esac)\b`)
	hostingEdgeQuotedSpan     = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)
	hostingEdgeEchoedStop     = regexp.MustCompile(`echo "((?:Not |Run this|Adopting)[^"]*)"`)
	hostingEdgeShellVariable  = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*`)
)

// hostingEdgeStub logs each call to $STUB_LOG, fails any call starting with $STUB_FAIL, and prints
// $STUB_STATE for `terraform state list`; every other call succeeds with no output.
const hostingEdgeStub = `#!/bin/sh
printf '%%s %%s\n' %[1]s "$*" >> "$STUB_LOG"
if [ -n "$STUB_FAIL" ]; then
  case "%[1]s $*" in "$STUB_FAIL"*) echo "%[1]s: simulated failure" >&2; exit 1 ;; esac
fi
case "%[1]s $1 $2" in "terraform state list") [ -z "$STUB_STATE" ] || printf '%%s\n' "$STUB_STATE" ;; esac
exit 0
`

// hostingEdgeStubTools writes a stub for each named tool into a fresh directory.
func hostingEdgeStubTools(t *testing.T, tools ...string) string {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range tools {
		if err := os.WriteFile(filepath.Join(bin, tool), fmt.Appendf(nil, hostingEdgeStub, tool), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// hostingEdgeStatements joins the flow's lines into shell statements: a line continues while it
// ends in a backslash or a pipe, or leaves a single-quoted jq program open. Comments are dropped.
func hostingEdgeStatements(flow string) []string {
	var statements []string
	var current strings.Builder
	for _, line := range strings.Split(flow, "\n") {
		trimmed := strings.TrimSpace(line)
		if current.Len() == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		current.WriteString(line + "\n")
		if strings.Count(current.String(), "'")%2 == 1 || strings.HasSuffix(trimmed, `\`) || strings.HasSuffix(trimmed, "|") {
			continue
		}
		statements = append(statements, current.String())
		current.Reset()
	}
	return statements
}

// hostingEdgeUnquoted blanks quoted text that runs no command, so words in messages and jq programs are not read as calls.
func hostingEdgeUnquoted(statement string) string {
	return hostingEdgeQuotedSpan.ReplaceAllStringFunc(statement, func(span string) string {
		if strings.Contains(span, "$(") {
			return span
		}
		return `""`
	})
}

// hostingEdgeRunFlow runs the plan-and-apply block under sh in dir with the given arguments and environment.
func hostingEdgeRunFlow(t *testing.T, flow, dir string, args, env []string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{"-c", flow, "sh"}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// hostingEdgeCalledTools returns the tools the flow runs as commands.
func hostingEdgeCalledTools(flow string) map[string]bool {
	called := map[string]bool{}
	for _, statement := range hostingEdgeStatements(flow) {
		for _, match := range hostingEdgeToolCall.FindAllStringSubmatch(hostingEdgeUnquoted(statement), -1) {
			called[match[1]] = true
		}
	}
	return called
}

// hostingEdgeToolCheck returns the Prerequisites block that checks tools with command -v.
func hostingEdgeToolCheck(t *testing.T) string {
	t.Helper()
	_, prerequisites, _ := strings.Cut(readRepoText(t, hostingEdgeSkillPath), "\n## Prerequisites")
	prerequisites, _, _ = strings.Cut(prerequisites, "\n## ")
	for _, block := range strings.Split(prerequisites, "```sh\n")[1:] {
		if block, _, _ = strings.Cut(block, "```"); strings.Contains(block, "command -v") {
			return block
		}
	}
	t.Fatal("the Prerequisites no longer check any tool with command -v")
	return ""
}

// hostingEdgeRunToolCheck runs the tool check with only the given tools on PATH.
func hostingEdgeRunToolCheck(t *testing.T, check string, tools ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", check)
	cmd.Env = []string{"PATH=" + hostingEdgeStubTools(t, tools...), "STUB_LOG=" + filepath.Join(t.TempDir(), "log")}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestHostingEdgeSkillChecksForEveryToolItsFlowRuns(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	called, check, all := hostingEdgeCalledTools(flow), hostingEdgeToolCheck(t), strings.Fields(hostingEdgeStubbedTools)
	if out, err := hostingEdgeRunToolCheck(t, check, all...); err != nil {
		t.Errorf("with every tool present the prerequisite check fails: %v: %s", err, out)
	}
	for _, tool := range all {
		if !called[tool] {
			t.Errorf("the Apply flow no longer runs %s; drop it from hostingEdgeStubbedTools", tool)
			continue
		}
		out, err := hostingEdgeRunToolCheck(t, check, strings.Fields(strings.ReplaceAll(" "+hostingEdgeStubbedTools+" ", " "+tool+" ", " "))...)
		if err == nil || !strings.Contains(out, tool) {
			t.Errorf("without %s on PATH the prerequisite check exits %v saying %q: the flow runs %s, so a host without it must stop here, naming it", tool, err, strings.TrimSpace(out), tool)
		}
	}
}

// hostingEdgeFaultRun runs the flow in a scratch root against stub tools: the kept state lists
// state, and every call starting with fail fails. It returns the calls made and how the run ended.
func hostingEdgeFaultRun(t *testing.T, flow, state, fail string) (string, error) {
	t.Helper()
	dir, log := t.TempDir(), filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(filepath.Join(dir, "erun-edge.tf"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + hostingEdgeStubTools(t, strings.Fields(hostingEdgeStubbedTools)...) + ":/usr/bin:/bin",
		"HOME=" + t.TempDir(), "STUB_LOG=" + log, "STUB_FAIL=" + fail, "STUB_STATE=" + state,
	}
	out, err := hostingEdgeRunFlow(t, flow, dir, []string{"-var", "services_zone=services.example.com"}, env)
	calls, _ := os.ReadFile(log)
	return string(calls) + out, err
}

// A failed terraform, jq or helm call must stop the run: read as empty output it says "no edge"
// or "nothing unsafe", and the gate is skipped on exactly the runs it exists to stop.
func TestHostingEdgeSkillStopsWhenACallItsGateReadsFails(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	for _, tc := range []struct{ name, state, fail string }{
		{"an edge the kept state holds, nothing failing", "helm_release.issuer", ""},
		{"a cluster with no edge, nothing failing", "", ""},
		{"terraform show failing", "", "terraform show"},
		{"terraform state list failing", "", "terraform state list"},
		{"jq failing on an edge the kept state holds", "helm_release.issuer", "jq"},
		{"jq failing with no kept state", "", "jq"},
		{"helm failing with no kept state", "", "helm"},
	} {
		calls, err := hostingEdgeFaultRun(t, flow, tc.state, tc.fail)
		applied, imported := strings.Contains(calls, hostingEdgeAppliedPlan), strings.Contains(calls, "terraform import")
		if tc.fail == "" && (err != nil || !applied) {
			t.Errorf("%s: the flow did not reach its apply (%v):\n%s", tc.name, err, calls)
		}
		if tc.fail != "" && (err == nil || applied || imported) {
			t.Errorf("%s: the flow exited %v, applied %t, imported %t: a failed call must stop the run before it imports or applies, not read as no edge or an empty gate\n%s", tc.name, err, applied, imported, calls)
		}
	}
}

func TestHostingEdgeSkillEndsEveryToolCallOnFailure(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	for _, statement := range hostingEdgeStatements(flow) {
		text := strings.TrimSpace(statement)
		if text == hostingEdgeAppliedPlan || !hostingEdgeToolCall.MatchString(hostingEdgeUnquoted(statement)) {
			continue
		}
		if !hostingEdgeStopsOnFailure.MatchString(text) {
			t.Errorf("the Apply flow runs this without stopping when it fails, so a broken tool or an unreachable cluster reads as an answer:\n%s", text)
		}
	}
}

func TestHostingEdgeSkillChecksForDestroysOnEveryRun(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	depth := 0
	for _, statement := range hostingEdgeStatements(flow) {
		if strings.Contains(statement, `index("delete")`) {
			if depth != 0 {
				t.Errorf("the in-place gate sits inside a conditional (depth %d): a delete is possible whenever the kept state holds the edge, so the gate must run on every run, not only when an issuer release is found by name", depth)
			}
			return
		}
		for _, match := range hostingEdgeShellKeyword.FindAllStringSubmatch(hostingEdgeUnquoted(statement), -1) {
			switch match[1] {
			case "if", "for", "while", "until", "case":
				depth++
			default:
				depth--
			}
		}
	}
	t.Fatal(`the Apply flow has no gate refusing a plan that deletes (index("delete"))`)
}

func TestHostingEdgeSkillStopsOutsideTheSetupShell(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	_, apply, _ := strings.Cut(readRepoText(t, hostingEdgeSkillPath), "\n## Apply\n")
	if apply, _, _ = strings.Cut(apply, "\n## "); !strings.Contains(strings.Join(strings.Fields(apply), " "), "The blocks run in one shell session.") {
		t.Error("the Apply section no longer says its blocks run in one shell session; the plan-and-apply block uses the directory, exports and set -- inputs the setup block leaves")
	}
	for _, tc := range []struct {
		name string
		root bool
		args []string
	}{
		{"a fresh shell outside the scratch root", false, []string{"-var", "services_zone=services.example.com"}},
		{"a fresh shell in the root without the edge inputs", true, nil},
	} {
		dir, log := t.TempDir(), filepath.Join(t.TempDir(), "log")
		if tc.root {
			if err := os.WriteFile(filepath.Join(dir, "erun-edge.tf"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		env := []string{"PATH=" + hostingEdgeStubTools(t, strings.Fields(hostingEdgeStubbedTools)...) + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "STUB_LOG=" + log}
		out, err := hostingEdgeRunFlow(t, flow, dir, tc.args, env)
		calls, _ := os.ReadFile(log)
		if err == nil || len(calls) != 0 {
			t.Errorf("%s: the plan-and-apply block exited %v after calling\n%s\nit must stop before running anything, saying why: %s", tc.name, err, calls, out)
		}
	}
}

func TestHostingEdgeSkillRefusesAnEdgeATenantTreeOwns(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	// erun terraform keeps a tree's state at ~/.erun/terraform/<tenant>/<env>/terraform.tfstate.
	terraform := readRepoText(t, "erun-common/command/terraform/terraform.go")
	for _, layout := range []string{`filepath.Join(home, ".erun", "terraform", tenant, environment)`, `filepath.Join(workDir, "terraform.tfstate")`} {
		if !strings.Contains(terraform, layout) {
			t.Errorf("erun terraform no longer keeps a tree's state where %s says (%s): move the skill's tenant-tree check with it", hostingEdgeTreeState, layout)
		}
	}
	tree := strings.Index(flow, hostingEdgeTreeState)
	confirm := strings.Index(flow, hostingEdgeAdoptConfirm)
	imports := strings.Index(flow, "terraform import")
	switch {
	case tree < 0:
		t.Fatalf("the Apply flow never reads %s: it adopts an edge a tenant tree's state already owns, and two states then own one edge", hostingEdgeTreeState)
	case confirm < 0:
		t.Fatalf("the Apply flow adopts without the operator's confirmation (%s): another environment's tree state cannot be seen from here", hostingEdgeAdoptConfirm)
	case imports < 0 || tree > imports || confirm > imports:
		t.Fatal("the Apply flow must refuse a tree-owned edge and wait for the operator's confirmation before its first terraform import: imports persist even when a later check stops the run")
	}
	refusal := flow[tree:imports]
	if !strings.Contains(refusal, `[ "$owned" = false ] ||`) {
		t.Error("the tenant-tree check must refuse on anything but a definite false, so a state it cannot read stops the run")
	}
	if !strings.Contains(refusal, "erun terraform apply") {
		t.Error("the tenant-tree refusal must name erun terraform apply, the path that maintains that edge")
	}
}

func TestSkillsSpecQuotesTheHostingEdgeStops(t *testing.T) {
	_, flow := hostingEdgeApplyBlocks(t)
	_, entry, found := strings.Cut(readRepoText(t, "erun-docs/docs/agent-reference/skills-spec.md"), "### `erun-enable-hosting-edge`\n")
	if !found {
		t.Fatal("skills-spec.md has no erun-enable-hosting-edge entry")
	}
	entry, _, _ = strings.Cut(entry, "\n### ")
	messages := hostingEdgeEchoedStop.FindAllStringSubmatch(flow, -1)
	if len(messages) == 0 {
		t.Fatal("the Apply flow echoes no stop message")
	}
	for _, message := range messages {
		for _, fragment := range hostingEdgeShellVariable.Split(message[1], -1) {
			if fragment = strings.TrimSpace(fragment); len(fragment) > 3 && !strings.Contains(entry, fragment) {
				t.Errorf("the skills-spec.md erun-enable-hosting-edge entry does not quote %q from the skill's stop %q", fragment, message[1])
			}
		}
	}
}
