package eruncommon

import (
	"fmt"
	"strings"
)

// rulesetBypassNotice is GitHub's own report, written to `git push`'s stderr,
// that a push was admitted by a ruleset bypass rather than by satisfying the
// rules. Ref is the ref the notice names (empty when GitHub did not name one),
// and Rules are the violated rules GitHub lists beneath it.
//
// The notice is the only place a non-bypassing operator learns that a
// protected branch was written through rather than around: GitHub attributes
// the bypass to a credential's grant, not to a person, so the message names
// the rules that were stepped over and nothing about who stepped over them.
// Who can be resolved afterwards, from the repository's rule-suites ledger
// (`erun exec reconcile-bypass`); that a push bypassed at all is knowable
// here, at the moment it happened, and nowhere else in erun's own output.
type rulesetBypassNotice struct {
	Ref   string
	Rules []string
}

// bypassNoticeMarker is the phrase GitHub opens the notice with. The rest of
// that line is "for <ref>:" or ":", depending on the ruleset's scope.
const bypassNoticeMarker = "Bypassed rule violations"

// parseRulesetBypassNotice reads GitHub's bypass notice out of a `git push`'s
// stderr. It reports false when stderr carries no notice, so a caller can ask
// unconditionally and a push that satisfied every rule costs nothing.
//
// Git relays the remote's own lines with a "remote: " prefix, and an
// unindented continuation of the notice carries no prefix of its own; both
// forms are accepted rather than requiring the prefix, because the prefix is
// git's formatting of someone else's message and not part of the contract.
func parseRulesetBypassNotice(stderr string) (rulesetBypassNotice, bool) {
	lines := strings.Split(stderr, "\n")
	for i, raw := range lines {
		line := remoteLineText(raw)
		if !strings.HasPrefix(line, bypassNoticeMarker) {
			continue
		}
		notice := rulesetBypassNotice{Ref: bypassNoticeRef(line)}
		for _, follow := range lines[i+1:] {
			next := remoteLineText(follow)
			// GitHub separates the listed rules with blank remote lines; a
			// blank line is not the end of the notice, but anything that is
			// neither blank nor a rule is.
			if next == "" {
				continue
			}
			rule, ok := strings.CutPrefix(next, "- ")
			if !ok {
				break
			}
			if rule = strings.TrimSpace(rule); rule != "" {
				notice.Rules = append(notice.Rules, rule)
			}
		}
		return notice, true
	}
	return rulesetBypassNotice{}, false
}

// remoteLineText strips git's "remote: " relay prefix and surrounding
// whitespace, leaving the message the remote actually wrote.
func remoteLineText(line string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "remote:"))
}

// bypassNoticeRef reads the ref out of a notice's opening line, which reads
// "Bypassed rule violations for refs/heads/main:". A ruleset that scopes no
// ref leaves it empty rather than inventing one.
func bypassNoticeRef(line string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(line, bypassNoticeMarker))
	rest = strings.TrimSpace(strings.TrimSuffix(rest, ":"))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "for "))
	return rest
}

// reportRulesetBypass reports a bypass a push was admitted by. It is not a
// failure and never makes the push one: the push landed, and the operator's
// business is knowing that it landed through a grant rather than through the
// rules. Saying so where the push itself is reported is the point -- the only
// other trace is GitHub's own remote line, which erun captures and used to
// discard on success.
func reportRulesetBypass(ctx Context, remote string, notice rulesetBypassNotice) {
	target := "this repository's ruleset"
	if notice.Ref != "" {
		target = "the ruleset protecting " + notice.Ref
	}
	rules := "the rules it enforces"
	if len(notice.Rules) > 0 {
		rules = strings.Join(notice.Rules, "; ")
	}
	ctx.Info(fmt.Sprintf("push: %s admitted this push by bypassing %s -- the credential in use holds a bypass grant, so these were not satisfied: %s", remote, target, rules))
	ctx.Info("push: run `erun exec reconcile-bypass` to check a bypassed push against the gate run that accounts for it.")
}
