package gitx

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// rulesetBypassNotice is GitHub's stderr report that a push was admitted by a ruleset
// bypass; it is the only moment erun can see that a bypass happened.
type rulesetBypassNotice struct {
	Ref   string
	Rules []string
}

// bypassNoticeMarker opens the notice; the line continues "for <ref>:" or ":".
const bypassNoticeMarker = "Bypassed rule violations"

// parseRulesetBypassNotice accepts lines with or without git's "remote: " prefix.
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
			// Blank lines separate rules; any other non-rule line ends the notice.
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

func remoteLineText(line string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "remote:"))
}

// bypassNoticeRef returns "" when the ruleset scopes no ref.
func bypassNoticeRef(line string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(line, bypassNoticeMarker))
	rest = strings.TrimSpace(strings.TrimSuffix(rest, ":"))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "for "))
	return rest
}

// reportRulesetBypass reports a bypass without failing the push.
func reportRulesetBypass(ctx core.Context, remote string, notice rulesetBypassNotice) {
	target := "this repository's ruleset"
	if notice.Ref != "" {
		target = "the ruleset protecting " + notice.Ref
	}
	rules := "the rules it enforces"
	if len(notice.Rules) > 0 {
		rules = strings.Join(notice.Rules, "; ")
	}
	ctx.Info(fmt.Sprintf("push: %s admitted this push by bypassing %s -- the credential in use holds a bypass grant, so these were not satisfied: %s", remote, target, rules))
	ctx.Info("push: run `erun exec reconcile-bypass` to check a bypassed push against the successful build that accounts for it.")
}

// ReportRulesetBypassFromPushArgs reports a bypass for callers holding push args rather than the remote.
func ReportRulesetBypassFromPushArgs(ctx core.Context, args []string, stderr string) {
	notice, ok := parseRulesetBypassNotice(stderr)
	if !ok {
		return
	}
	reportRulesetBypass(ctx, pushRemoteFromArgs(args), notice)
}

// pushRemoteFromArgs takes the first non-option after `push`; erun passes options as
// --flag=value so a separate value is never mistaken for the remote.
func pushRemoteFromArgs(args []string) string {
	for _, arg := range args[min(1, len(args)):] {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return "origin"
}
