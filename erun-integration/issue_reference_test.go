package integration

// issue_reference_test.go is a repo-state structural gate for root
// AGENTS.md § "Code Comments", which asks that a tracker reference never
// reach a comment because provenance lives in the git history (git blame),
// not the source. Eleven separate agent lanes wrote a tracker reference into a
// comment or a subtest name in one session, every one of them given that
// rule verbatim in its prompt, because the rule lived only in a prompt: it
// depended on every dispatcher restating it and every author following it.
// This is the structural fix, the same shape as bare_required_input_test.go
// and desktop_surface_test.go: an AST walk that fails the build the next
// time the defect reappears, rather than another instruction to ignore.
//
// A prior gate for a different defect class was written against two exact
// literal phrasings and was walked through by a third phrasing within
// hours (see bare_required_input_test.go's own history). issueReferencePattern
// below is written against the *shape* of a tracker reference instead: a
// bare "#" followed by digits, an "issue #" prefix, an "owner/repo#" cross-
// repo form, or a full GitHub issue/pull URL -- so a new prefix word or a
// different repo does not slip past it the way a fixed string would.
//
// Scope is deliberately narrow: comments, function/method declaration names,
// and t.Run/b.Run subtest names -- "comments... and test names" is the
// defect this was filed for. An arbitrary string literal (a t.Errorf/t.Skip
// diagnostic message, a user-facing error string) is not scanned; those are
// runtime output, not source documentation, and a blanket string-literal
// scan would flag legitimate content (a URL quoted in a fixture, a golden
// value) far more often than it would catch a real instance of this defect.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// issueReferencePattern matches a GitHub issue/PR reference by shape, not by
// one fixed phrasing:
//   - a bare hash sign directly followed by a number
//   - the word "issue" (any case) before a hash-number, whitespace-tolerant
//   - a cross-repo "owner/repo" path directly followed by a hash-number
//   - a full GitHub issue or pull-request URL ("github.com/<owner>/<repo>/issues/<n>"
//     or ".../pull/<n>")
//
// The bare hash-number alternative is intentionally the widest of the four:
// it is what actually catches a new prefix wording this pattern was never
// told about, at the cost of also matching a rare non-issue use of "#" next
// to digits (e.g. a numeric hex color in a comment). That tradeoff mirrors
// bareRequiredInputPattern's own "generalize the shape, accept the rare
// coincidental match" choice, because the alternative -- enumerating
// phrasings -- is the exact failure this gate exists to not repeat.
var issueReferencePattern = regexp.MustCompile(`(?i)issue\s*#\s*\d+|[\w][\w.-]*/[\w][\w.-]*#\d+|(?:^|[^\w#])(#\d{2,6})\b|github\.com/[\w.-]+/[\w.-]+/(?:issues|pull)/\d+`)

// matchIssueReference returns the offending substring issueReferencePattern
// matched in text, or "" if it did not match. The bare hash-number
// alternative above must consume the character before the "#" to enforce a
// word boundary (RE2 has no lookbehind), so it captures just the "#NNN"
// portion in group 1; every other alternative's own match is already exactly
// the reference text, so group 1 being empty falls back to the whole match.
func matchIssueReference(text string) string {
	m := issueReferencePattern.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[0]
}

// issueReferenceIdentPattern matches the identifier-shape variant of the
// same defect: a function or method name that spells out "issue" next to a
// number (e.g. a test named TestIssue1470Regression). Go identifiers cannot
// contain "#" or "/", so the bare-hash and URL shapes above do not apply
// here; a plain number embedded in a name (e.g. Test1470) is not enough on
// its own to flag; too many legitimately numbered identifiers exist for
// that to be a useful signal. Requiring the word "issue" keeps this to the
// same "this identifier literally names a tracker reference" shape.
var issueReferenceIdentPattern = regexp.MustCompile(`(?i)issue[_]?\d{2,6}`)

// skipDirForIssueReferenceScan excludes directories that hold no
// hand-written production or test Go source: version control, installed JS
// dependencies, generated/vendored trees, and per-command golden fixtures
// (testdata/ captures real command output, which may legitimately include
// operator-authored text containing a "#"; it holds no .go source of its
// own in this repo today, but is excluded on principle rather than by
// accident of the current tree).
func skipDirForIssueReferenceScan(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "wailsjs", ".claude", ".claude-plugin", ".vscode", "testdata":
		return true
	default:
		return false
	}
}

// issueReferenceHit is one tracker reference found in Go source.
type issueReferenceHit struct {
	position string
	file     string // path relative to the scan root, forward-slash separated
	kind     string // "comment", "function name", or "subtest name"
	value    string
}

func (h issueReferenceHit) Message() string {
	return fmt.Sprintf("%s: %s contains a tracker reference %q -- issue references belong in the PR body, not in code (root AGENTS.md § \"Code Comments\")", h.position, h.kind, h.value)
}

// findIssueReferenceHits parses every .go file under root (production and
// test files alike -- test names are an explicit target of this gate) and
// returns one hit per comment matching issueReferencePattern, per function
// or method declaration matching issueReferenceIdentPattern, and per
// t.Run/b.Run subtest name literal matching issueReferencePattern.
// AST-based rather than a text grep so a match inside an unrelated string
// literal (a t.Errorf message, a golden value) does not fail the gate --
// only source that is genuinely a comment, a declared name, or a subtest
// title counts.
func findIssueReferenceHits(t testing.TB, root string) []issueReferenceHit {
	t.Helper()
	var hits []issueReferenceHit
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root && skipDirForIssueReferenceScan(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("relativize %s: %w", path, relErr)
		}
		rel = filepath.ToSlash(rel)

		for _, cg := range file.Comments {
			for _, c := range cg.List {
				if m := matchIssueReference(c.Text); m != "" {
					hits = append(hits, issueReferenceHit{
						position: fset.Position(c.Pos()).String(),
						file:     rel,
						kind:     "comment",
						value:    m,
					})
				}
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				if issueReferenceIdentPattern.MatchString(node.Name.Name) {
					hits = append(hits, issueReferenceHit{
						position: fset.Position(node.Name.Pos()).String(),
						file:     rel,
						kind:     "function name",
						value:    node.Name.Name,
					})
				}
			case *ast.CallExpr:
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Run" || len(node.Args) == 0 {
					return true
				}
				lit, ok := node.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					return true
				}
				if m := matchIssueReference(value); m != "" {
					hits = append(hits, issueReferenceHit{
						position: fset.Position(lit.Pos()).String(),
						file:     rel,
						kind:     "subtest name",
						value:    m,
					})
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan repo for issue reference hits: %v", err)
	}
	return hits
}

// issueReferenceBaseline is a shrink-only baseline (the same pattern as
// bareRequiredInputBaseline above and KnownUnsurfacedRoutes in
// erun-backend-api/internal/routes/route_audit.go) for the pre-existing
// tracker references this gate found on adoption: 345 hits across 145
// files, almost all of them explanatory comments written before this rule
// was enforceable structurally. Rewriting all of them is a distinct, large
// cleanup with no behavior change of its own, not something to rush inline
// with the gate that now prevents new ones. This baseline may only shrink:
// fixing a site and forgetting to remove its entry here fails
// TestIssueReferenceBaselineIsCurrent below, the same way a stale
// KnownUnsurfacedRoutes entry fails its own gate. The key is the file path
// relative to the repo root; the value is the exact count of matching hits
// still in that file.
var issueReferenceBaseline = map[string]int{
	"erun-backend/erun-backend-api/comments_e2e_test.go":                            1,
	"erun-backend/erun-backend-api/env_deploy_e2e_test.go":                          2,
	"erun-backend/erun-backend-api/environment_delete_e2e_test.go":                  3,
	"erun-backend/erun-backend-api/env_provisioner_rbac_e2e_test.go":                8,
	"erun-backend/erun-backend-api/internal/deployexec/job.go":                      4,
	"erun-backend/erun-backend-api/internal/deployexec/job_test.go":                 1,
	"erun-backend/erun-backend-api/internal/deployexec/lifecycle_job.go":            4,
	"erun-backend/erun-backend-api/internal/deployexec/lifecycle_job_test.go":       1,
	"erun-backend/erun-backend-api/internal/deployexec/placement.go":                3,
	"erun-backend/erun-backend-api/internal/deployexec/placement_test.go":           1,
	"erun-backend/erun-backend-api/internal/model/context.go":                       1,
	"erun-backend/erun-backend-api/internal/model/environment.go":                   3,
	"erun-backend/erun-backend-api/internal/model/invite.go":                        1,
	"erun-backend/erun-backend-api/internal/model/tenant_quota.go":                  1,
	"erun-backend/erun-backend-api/internal/model/usage_event.go":                   1,
	"erun-backend/erun-backend-api/internal/provision/delete.go":                    2,
	"erun-backend/erun-backend-api/internal/provision/delete_reconciler.go":         6,
	"erun-backend/erun-backend-api/internal/provision/delete_reconciler_test.go":    4,
	"erun-backend/erun-backend-api/internal/provision/environments.go":              1,
	"erun-backend/erun-backend-api/internal/provision/environments_test.go":         1,
	"erun-backend/erun-backend-api/internal/provision/lifecycle.go":                 6,
	"erun-backend/erun-backend-api/internal/provision/lifecycle_test.go":            4,
	"erun-backend/erun-backend-api/internal/provision/registry_credentials_test.go": 1,
	"erun-backend/erun-backend-api/internal/repository/environments.go":             14,
	"erun-backend/erun-backend-api/internal/repository/invites_e2e_test.go":         3,
	"erun-backend/erun-backend-api/internal/repository/invites.go":                  1,
	"erun-backend/erun-backend-api/internal/repository/reviews_e2e_test.go":         1,
	"erun-backend/erun-backend-api/internal/repository/tenant_quotas.go":            3,
	"erun-backend/erun-backend-api/internal/repository/tenant_quotas_test.go":       3,
	"erun-backend/erun-backend-api/internal/routes/config_test.go":                  3,
	"erun-backend/erun-backend-api/internal/routes/contexts.go":                     1,
	"erun-backend/erun-backend-api/internal/routes/contexts_test.go":                1,
	"erun-backend/erun-backend-api/internal/routes/environments.go":                 21,
	"erun-backend/erun-backend-api/internal/routes/environments_test.go":            13,
	"erun-backend/erun-backend-api/internal/routes/identity.go":                     1,
	"erun-backend/erun-backend-api/internal/routes/identity_test.go":                3,
	"erun-backend/erun-backend-api/internal/routes/invites.go":                      4,
	"erun-backend/erun-backend-api/internal/routes/invites_test.go":                 2,
	"erun-backend/erun-backend-api/internal/routes/provision.go":                    3,
	"erun-backend/erun-backend-api/internal/routes/tenant_quotas.go":                2,
	"erun-backend/erun-backend-api/internal/routes/tenant_quotas_test.go":           1,
	"erun-backend/erun-backend-api/internal/service/environments.go":                3,
	"erun-backend/erun-backend-api/internal/service/environments_test.go":           3,
	"erun-backend/erun-backend-api/internal/service/identity.go":                    2,
	"erun-backend/erun-backend-api/internal/service/identity_test.go":               1,
	"erun-backend/erun-backend-api/internal/service/invites_test.go":                2,
	"erun-backend/erun-backend-api/internal/zitadel/client_e2e_test.go":             2,
	"erun-backend/erun-backend-api/internal/zitadel/client_test.go":                 1,
	"erun-backend/erun-backend-api/internal/zitadel/policies_test.go":               1,
	"erun-backend/erun-backend-api/internal/zitadel/smtp.go":                        2,
	"erun-backend/erun-backend-api/registry_token_route_test.go":                    1,
	"erun-backend/erun-backend-api/server.go":                                       7,
	"erun-cli/cmd/doctor_host_credentials.go":                                       1,
	"erun-cli/cmd/job.go":                                 1,
	"erun-cli/cmd/mcp_proxy.go":                           2,
	"erun-cli/cmd/review.go":                              1,
	"erun-common/activity_lease_test.go":                  3,
	"erun-common/build_run.go":                            1,
	"erun-common/build_run_test.go":                       1,
	"erun-common/deploy.go":                               1,
	"erun-common/deploy_image_pull_secret.go":             1,
	"erun-common/environment_type_test.go":                1,
	"erun-common/ghcr_credential_preflight.go":            1,
	"erun-common/ghcr_credential_preflight_test.go":       1,
	"erun-common/hosted_registry_test.go":                 1,
	"erun-common/init_remote.go":                          1,
	"erun-common/job_supervisor.go":                       2,
	"erun-common/job_test.go":                             1,
	"erun-common/kubernetes_namespace_conditions_test.go": 1,
	"erun-common/kubernetes_namespace.go":                 2,
	"erun-common/kubernetes_resource_quota.go":            1,
	"erun-common/kubernetes_resource_quota_test.go":       1,
	"erun-common/mcp_capabilities.go":                     1,
	"erun-common/mcp_reachability.go":                     1,
	"erun-common/mcp_tools_test.go":                       1,
	"erun-common/observe_drift.go":                        1,
	"erun-common/platform_client.go":                      2,
	"erun-common/platform_client_reviews.go":              1,
	"erun-common/platform_commands.go":                    1,
	"erun-common/published_devops_chart.go":               3,
	"erun-common/published_devops_chart_test.go":          2,
	"erun-common/runtime_resources.go":                    4,
	"erun-common/runtime_usage.go":                        1,
	"erun-common/runtime_usage_test.go":                   1,
	"erun-common/upgrade_resolver_test.go":                6,
	"erun-devops/dns01-webhook/solver.go":                 1,
	"erun-devops/dns01-webhook/solver_test.go":            1,
	"erun-integration/activity_test.go":                   1,
	"erun-integration/build_test.go":                      1,
	"erun-integration/delete_test.go":                     3,
	"erun-integration/deploy_test.go":                     6,
	"erun-integration/doctor_test.go":                     6,
	"erun-integration/environment_half_scenarios_test.go": 5,
	"erun-integration/exec_test.go":                       1,
	"erun-integration/init_test.go":                       3,
	"erun-integration/observe_test.go":                    5,
	"erun-integration/push_test.go":                       8,
	"erun-integration/release_test.go":                    3,
	"erun-integration/usage_test.go":                      1,
	"erun-integration/version_test.go":                    2,
	"erun-mcp/agent.go":                                   1,
	"erun-mcp/capabilities.go":                            1,
	"erun-mcp/capabilities_test.go":                       1,
	"erun-mcp/exec.go":                                    1,
	"erun-mcp/job_envelope.go":                            1,
	"erun-mcp/job_target_test.go":                         1,
	"erun-mcp/mcp_overview_doc_test.go":                   1,
	"erun-mcp/platform.go":                                1,
	"erun-mcp/runtime.go":                                 1,
	"erun-mcp/server.go":                                  5,
	"erun-mcp/server_test.go":                             3,
	"erun-mcp/tool_metadata_test.go":                      1,
	"erun-ui/api_log_test.go":                             1,
	"erun-ui/app_test.go":                                 5,
	"erun-ui/config_handlers.go":                          1,
	"erun-ui/env_ensure_test.go":                          2,
	"erun-ui/environment_activity_observed_test.go":       1,
	"erun-ui/host_open_path.go":                           1,
	"erun-ui/host_open_path_test.go":                      1,
	"erun-ui/mcp_errors.go":                               1,
	"erun-ui/orchestrator.go":                             2,
	"erun-ui/orchestrator_guidance_test.go":               1,
	"erun-ui/orchestrator_mcp.go":                         2,
	"erun-ui/orchestrator_mcp_test.go":                    2,
	"erun-ui/orchestrator_pacing.go":                      1,
	"erun-ui/orchestrator_pacing_test.go":                 1,
	"erun-ui/orchestrator_role_file_test.go":              1,
	"erun-ui/orchestrator_shell_activity.go":              1,
	"erun-ui/orchestrator_shell_activity_test.go":         1,
	"erun-ui/orchestrator_test.go":                        3,
	"erun-ui/restart_control_test.go":                     1,
	"erun-ui/session_heartbeat_test.go":                   1,
	"erun-ui/tenant_dashboard.go":                         3,
	"erun-ui/tenant_dashboard_test.go":                    1,
	"erun-ui/tenant_review_detail.go":                     1,
	"erun-ui/tenant_review_detail_test.go":                1,
	"erun-ui/terminal_repaint_input_test.go":              4,
	"erun-ui/terminal_sessions.go":                        3,
	"erun-ui/ui_model.go":                                 2,
}

// TestNoIssueReferenceInCode fails when a tracker reference matching
// issueReferencePattern or issueReferenceIdentPattern reappears in Go
// source anywhere in the repo, beyond what issueReferenceBaseline already
// carries for a given file. A file with no baseline entry gets zero
// tolerance -- any hit there is a brand new instance of the bug. A file
// with a baseline entry may not exceed it: that would be a new reference
// added next to the pre-existing ones, not cleaning them up.
func TestNoIssueReferenceInCode(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	counts := map[string]int{}
	for _, hit := range findIssueReferenceHits(t, root) {
		counts[hit.file]++
		if counts[hit.file] > issueReferenceBaseline[hit.file] {
			t.Errorf("%s", hit.Message())
		}
	}
}

// TestIssueReferenceBaselineIsCurrent fails when a baselined file's actual
// hit count has dropped below what issueReferenceBaseline still claims --
// the same shrink-only enforcement FindStaleBaselineEntries applies to
// KnownUnsurfacedRoutes and TestBareRequiredInputBaselineIsCurrent applies
// above. A cleanup that removes a reference without lowering its baseline
// entry here would otherwise let the debt silently look larger than it is
// forever.
//
// A baselined file that is absent from root entirely is skipped rather than
// compared: this walker only ever runs against whatever tree it was pointed
// at, and a narrowed tree (a container build context that COPYs a subset of
// the repo, for instance) legitimately does not contain every file a full
// checkout does. Zero hits from a file that is not there and zero hits from
// a file that was genuinely cleaned up are indistinguishable by count alone,
// so treating an absent file as "cleaned up" silently defeats the shrink-only
// contract for exactly the files a narrowed tree omits. Skipping instead
// means a genuinely stale entry still fails here on any tree that does
// contain the file -- including a full checkout -- so the contract holds
// everywhere the file exists to check.
func TestIssueReferenceBaselineIsCurrent(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	counts := map[string]int{}
	for _, hit := range findIssueReferenceHits(t, root) {
		counts[hit.file]++
	}
	for file, baseline := range issueReferenceBaseline {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("stat %s: %v", file, err)
		}
		if actual := counts[file]; actual < baseline {
			t.Errorf("%s: issueReferenceBaseline claims %d hit(s) but only %d remain -- lower the baseline entry", file, baseline, actual)
		}
	}
}

// TestFindIssueReferenceHitsExclusions locks the scope this scanner must
// respect: a tracker reference inside a real comment is caught (including
// inside a _test.go file, since test files are not excluded), a reference
// inside a t.Run subtest name is caught, a reference inside an unrelated
// string literal (a t.Errorf diagnostic) is not, and a markdown-style "#"
// heading with no digit is not. Every reference value below is a
// deliberately-constructed fixture using a number and repo name that do not
// correspond to any real GitHub issue, written into a temp directory rather
// than committed source.
func TestFindIssueReferenceHitsExclusions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		"production.go": `package fixture

// FIXTURE: deliberately-constructed violation for this test only.
// see #9999's precedent for how this used to resolve.
func doWork() error {
	return nil
}
`,
		"production_test.go": `package fixture

import "testing"

// FIXTURE: deliberately-constructed violation -- subtest names are in scope.
func TestSomething(t *testing.T) {
	t.Run("fixed in example-org/example-repo#9999", func(t *testing.T) {})
}
`,
		"unrelated.go": `package fixture

import "fmt"

// A markdown-style heading inside a comment, not a tracker reference.
// # Overview

func errFatalMessage() error {
	return fmt.Errorf("see https://github.com/example-org/example-repo/issues/9999 for docs")
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	hits := findIssueReferenceHits(t, dir)
	if len(hits) != 2 {
		t.Fatalf("expected exactly two hits (production.go's comment, production_test.go's subtest name), got %+v", hits)
	}

	byFile := map[string]issueReferenceHit{}
	for _, h := range hits {
		byFile[h.file] = h
	}

	commentHit, ok := byFile["production.go"]
	if !ok || commentHit.kind != "comment" || commentHit.value != "#9999" {
		t.Fatalf("expected production.go's comment hit %q, got %+v", "#9999", commentHit)
	}
	subtestHit, ok := byFile["production_test.go"]
	if !ok || subtestHit.kind != "subtest name" || subtestHit.value != "example-org/example-repo#9999" {
		t.Fatalf("expected production_test.go's subtest hit %q, got %+v", "example-org/example-repo#9999", subtestHit)
	}
	if _, ok := byFile["unrelated.go"]; ok {
		t.Fatalf("expected unrelated.go to have no hits (its reference sits inside an Errorf string, not a comment/name/subtest), got %+v", hits)
	}
}

// TestIssueReferencePatternCatchesShapeVariants is the regression for the
// design goal this gate exists to hold: match the shape of a tracker
// reference, not one fixed phrasing. Every "true" case here uses a
// deliberately fake org/repo and issue number.
func TestIssueReferencePatternCatchesShapeVariants(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"#9999", true},
		{"see #9999 for context", true},
		{"issue #9999", true},
		{"Issue #9999", true},
		{"issue#9999", true},
		{"example-org/example-repo#9999", true},
		{"https://github.com/example-org/example-repo/issues/9999", true},
		{"https://github.com/example-org/example-repo/pull/9999", true},
		{"# Overview", false},
		{"C# is a language", false},
		{"go 1.9999 is not a real toolchain version", false},
	} {
		if got := issueReferencePattern.MatchString(tc.value); got != tc.want {
			t.Errorf("issueReferencePattern.MatchString(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// TestIssueReferenceIdentPatternCatchesShapeVariants locks the
// identifier-shape variant: a declared name must spell out "issue" next to
// a number. A bare number in a name (Test1234) is not enough on its own --
// too many legitimately numbered identifiers exist for that to be useful.
func TestIssueReferenceIdentPatternCatchesShapeVariants(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"TestIssue9999Regression", true},
		{"handleIssue1234", true},
		{"issue_9999_fix", true},
		{"TestSomethingElse", false},
		{"Test1234", false},
	} {
		if got := issueReferenceIdentPattern.MatchString(tc.value); got != tc.want {
			t.Errorf("issueReferenceIdentPattern.MatchString(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
