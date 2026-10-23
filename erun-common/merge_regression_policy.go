package eruncommon

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The regression-test policy a gate-merge enforces before a source lands: a
// defect fix names the test case that reproduces the failure it fixes, or an
// exemption from a closed set. Root AGENTS.md § "A Defect Fix Names Its
// Reproduction" owns the rule; this decides only what is mechanically
// decidable — that the claim is named, specific, and resolves in the staged
// change. Whether the named case really reproduces the report is review's call.

var (
	mergeRegressionTestFile = regexp.MustCompile(`(_test\.go|\.(test|spec)\.(ts|tsx|mjs)|_test\.sh|\.tftest\.hcl)$`)
	mergeRegressionDocFile  = regexp.MustCompile(`(?i)\.(md|mdx|png|jpe?g|gif|svg|webp)$`)
	mergeRegressionRevert   = regexp.MustCompile(`(?m)^This reverts commit [0-9a-f]{7,40}\b`)
)

// mergeRegressionMinReason is the shortest no-reproducible-failure reason
// accepted: that kind is a bare claim, so it at least has to say something.
const mergeRegressionMinReason = 30

// evaluateMergeRegressionPolicy returns nil when the source is out of scope
// or its declaration holds, and otherwise an error naming every failure.
// bodies are the raw commit messages the source contributes, staged is what
// its squash staged, and readFile reads a repository-relative path from the
// squashed tree.
func evaluateMergeRegressionPolicy(branch string, bodies, staged []string, readFile func(string) ([]byte, error)) error {
	trailers := mergeRegressionTrailers(bodies)
	if !mergeRegressionInScope(branch, trailers["Defect-Fix"]) {
		return nil
	}
	var failures []string
	if len(trailers["Reproduces"]) == 0 {
		failures = append(failures, `a defect fix needs a "Reproduces:" trailer stating the failure the report described`)
	}
	failures = append(failures, mergeRegressionTestFailures(trailers, bodies, staged, readFile)...)
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%s (see root AGENTS.md § \"A Defect Fix Names Its Reproduction\")", strings.Join(failures, "; "))
}

// mergeRegressionInScope decides whether the source is a defect fix: a
// Defect-Fix trailer answers outright, and otherwise a bug/ branch is one.
func mergeRegressionInScope(branch string, declared []string) bool {
	for _, value := range declared {
		if v := strings.ToLower(value); v == "yes" || v == "true" {
			return true
		}
	}
	for _, value := range declared {
		if v := strings.ToLower(value); v == "no" || v == "false" {
			return false
		}
	}
	return strings.HasPrefix(strings.TrimPrefix(branch, "refs/heads/"), "bug/")
}

func mergeRegressionTestFailures(trailers map[string][]string, bodies, staged []string, readFile func(string) ([]byte, error)) []string {
	declared := trailers["Regression-Test"]
	if len(declared) == 0 {
		return []string{`no "Regression-Test:" trailer names the case that reproduces the failure (or "Regression-Test: none" with an exemption)`}
	}
	var named []string
	for _, value := range declared {
		if !strings.EqualFold(value, "none") {
			named = append(named, value)
		}
	}
	if len(named) == len(declared) {
		var failures []string
		for _, spec := range named {
			if problem := resolveMergeRegressionCase(spec, staged, true, readFile); problem != "" {
				failures = append(failures, "Regression-Test: "+problem)
			}
		}
		return failures
	}
	if len(named) > 0 {
		return []string{`"Regression-Test: none" and a named regression test are both declared; pick one`}
	}
	return mergeRegressionExemptionFailures(trailers, bodies, staged, readFile)
}

func mergeRegressionExemptionFailures(trailers map[string][]string, bodies, staged []string, readFile func(string) ([]byte, error)) []string {
	exemptions := trailers["Regression-Test-Exemption"]
	if len(exemptions) == 0 {
		return []string{`"Regression-Test: none" needs "Regression-Test-Exemption: <kind>: <reason>" with kind docs-only, revert, covered-by-existing, or no-reproducible-failure`}
	}
	kind, reason, _ := strings.Cut(exemptions[0], ":")
	kind, reason = strings.TrimSpace(kind), strings.TrimSpace(reason)
	if reason == "" {
		return []string{fmt.Sprintf("exemption %q gives no reason", kind)}
	}
	switch kind {
	case "docs-only":
		return mergeRegressionDocsOnlyFailures(staged)
	case "revert":
		return mergeRegressionRevertFailures(bodies)
	case "covered-by-existing":
		return mergeRegressionExistingFailures(trailers["Regression-Test-Existing"], staged, readFile)
	case "no-reproducible-failure":
		if len(reason) < mergeRegressionMinReason {
			return []string{fmt.Sprintf("exemption no-reproducible-failure needs a reason of at least %d characters", mergeRegressionMinReason)}
		}
		return nil
	default:
		return []string{fmt.Sprintf("unknown exemption kind %q; kinds are docs-only, revert, covered-by-existing, no-reproducible-failure", kind)}
	}
}

func mergeRegressionDocsOnlyFailures(staged []string) []string {
	for _, path := range staged {
		if !mergeRegressionDocFile.MatchString(path) {
			return []string{fmt.Sprintf("exemption docs-only, but the change stages the non-documentation file %s", path)}
		}
	}
	return nil
}

func mergeRegressionRevertFailures(bodies []string) []string {
	for _, body := range bodies {
		if mergeRegressionRevert.MatchString(body) {
			return nil
		}
	}
	return []string{`exemption revert, but no commit carries a "This reverts commit <sha>" line`}
}

func mergeRegressionExistingFailures(existing, staged []string, readFile func(string) ([]byte, error)) []string {
	if len(existing) == 0 {
		return []string{`exemption covered-by-existing needs "Regression-Test-Existing: <path>::<case>" naming the test that already reproduces it`}
	}
	var failures []string
	for _, spec := range existing {
		if problem := resolveMergeRegressionCase(spec, staged, false, readFile); problem != "" {
			failures = append(failures, "Regression-Test-Existing: "+problem)
		}
	}
	return failures
}

// resolveMergeRegressionCase checks that spec is <path>::<case>, that path is
// a test file (staged by this change when requireStaged), and that the file
// contains the case. It returns the problem, or "" when the case resolves.
func resolveMergeRegressionCase(spec string, staged []string, requireStaged bool, readFile func(string) ([]byte, error)) string {
	sep := strings.LastIndex(spec, "::")
	if sep < 0 {
		return fmt.Sprintf("%q is not in the <path>::<case> form", spec)
	}
	path, testCase := strings.TrimSpace(spec[:sep]), strings.TrimSpace(spec[sep+2:])
	if path == "" || testCase == "" {
		return fmt.Sprintf("%q must name both a path and a case", spec)
	}
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return fmt.Sprintf("%q is not a repository-relative path", path)
	}
	if !mergeRegressionTestFile.MatchString(path) {
		return fmt.Sprintf("%q is not a test file (*_test.go, *.test|spec.ts|tsx|mjs, *_test.sh, *.tftest.hcl)", path)
	}
	if requireStaged && !slices.Contains(staged, path) {
		return fmt.Sprintf("%q is not added or modified by this change; a reproduction is written here, or declared covered-by-existing", path)
	}
	content, err := readFile(path)
	if err != nil {
		return fmt.Sprintf("%q cannot be read: %v", path, err)
	}
	if !strings.Contains(string(content), testCase) {
		return fmt.Sprintf("%q does not contain the case %q", path, testCase)
	}
	return ""
}

// mergeRegressionTrailers reads the carried trailers out of every body into
// token → values, a wrapped value's lines joined by single spaces.
func mergeRegressionTrailers(bodies []string) map[string][]string {
	trailers := map[string][]string{}
	for _, body := range bodies {
		for _, entry := range gateMergeTrailersFromBody(body) {
			token, value, ok := strings.Cut(entry, ":")
			if !ok {
				continue
			}
			value = strings.Join(strings.Fields(value), " ")
			if value != "" {
				trailers[token] = append(trailers[token], value)
			}
		}
	}
	return trailers
}
