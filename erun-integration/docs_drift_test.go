package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// gateRunSignatureDocStopwords are filler and verb words stripped before
// checking that a known infrastructure-failure signature is reflected in the
// docs. erun-docs/docs/agent-reference/skills-spec.md paraphrases each
// signature in flowing prose rather than quoting it verbatim -- e.g. the code
// string "failed to fetch oauth token" appears in the doc as "a failed oauth
// token fetch" -- so this test checks for a signature's distinctive nouns
// rather than an exact substring, which a verbatim match would reject.
var gateRunSignatureDocStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "for": true,
	"on": true, "in": true, "failed": true, "fetch": true, "fetching": true,
	"resolve": true, "resolving": true, "resolution": true,
}

var docDriftWordPattern = regexp.MustCompile(`[a-z0-9]+`)

// normalizedWordSet lowercases text and splits it into a set of alphanumeric
// words, so hyphens, punctuation, and markdown formatting around a word never
// stop it from matching.
func normalizedWordSet(text string) map[string]bool {
	words := docDriftWordPattern.FindAllString(strings.ToLower(text), -1)
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// significantWords returns signature's words with the stopwords in
// gateRunSignatureDocStopwords removed, leaving the nouns a paraphrase cannot
// drop without losing the signature's meaning.
func significantWords(signature string) []string {
	words := docDriftWordPattern.FindAllString(strings.ToLower(signature), -1)
	var kept []string
	for _, w := range words {
		if !gateRunSignatureDocStopwords[w] {
			kept = append(kept, w)
		}
	}
	return kept
}

// TestGateRunInfrastructureSignaturesAreDocumented cross-checks erun-common's
// own known-infrastructure-failure signature list
// (eruncommon.GateRunInconclusiveSignatures, the exact strings
// RunGateRunStart/RunGateRunReport match before silently reclassifying a
// caller-reported FAILED verdict to INCONCLUSIVE) against
// erun-docs/docs/agent-reference/skills-spec.md, the one page that specs the
// classifier's exact trigger condition end to end (a doc-drift sweep found
// three *other* pages -- cli/exec.md, cli/review.md, mcp/overview.md -- that
// described the surrounding FAILED-report behavior with no mention of this
// reclassification at all, which this test cannot see: it only knows how to
// compare a code-owned list against one designated spec page's prose, not to
// judge whether every page that touches the topic says enough about it).
// What it does catch: the classifier's own trigger
// condition -- which failures get silently reclassified -- silently drifting
// from what the canonical spec page says it is, in either direction (a
// signature added to the code with no corresponding update to the doc, or a
// signature whose wording changed enough that the doc's description no
// longer matches).
func TestGateRunInfrastructureSignaturesAreDocumented(t *testing.T) {
	root := repoRoot(t)
	docPath := filepath.Join(root, "erun-docs", "docs", "agent-reference", "skills-spec.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	docWords := normalizedWordSet(string(data))

	signatures := eruncommon.GateRunInconclusiveSignatures()
	if len(signatures) == 0 {
		t.Fatal("eruncommon.GateRunInconclusiveSignatures() returned nothing; the rest of this test would pass vacuously")
	}

	var missing []string
	for _, sig := range signatures {
		for _, word := range significantWords(sig) {
			if !docWords[word] {
				missing = append(missing, fmt.Sprintf("%q (missing word %q)", sig, word))
				break
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s: known infrastructure-failure signature %s is not reflected in the classifier's spec -- "+
			"update the doc's description of gateRunInconclusiveSignatures (erun-common/gate_run_failure_classifier.go)",
			docPath, m)
	}
}
