package activity

import (
	"fmt"
	"regexp"
	"strings"
)

// An MCP edge older than the exclusive-claim release rejects its arguments with a raw
// schema error that reads like a malformed call; this file names the version skew instead.

// Matches the go-sdk's `unexpected additional properties ["name" ...]`.
var mcpUnexpectedAdditionalPropertiesPattern = regexp.MustCompile(`unexpected additional properties \[([^\]]*)\]`)

// mcpUnexpectedAdditionalProperties reports false for any other error, so a malformed
// call is never reframed as version skew.
func mcpUnexpectedAdditionalProperties(err error) ([]string, bool) {
	if err == nil {
		return nil, false
	}
	match := mcpUnexpectedAdditionalPropertiesPattern.FindStringSubmatch(err.Error())
	if match == nil {
		return nil, false
	}
	var names []string
	for _, quoted := range strings.Fields(match[1]) {
		if name := strings.Trim(quoted, `"`); name != "" {
			names = append(names, name)
		}
	}
	return names, len(names) > 0
}

var exclusiveClaimVersionSkewArguments = map[string]bool{
	"exclusive":    true,
	"orchestrator": true,
}

// DescribeExclusiveClaimVersionSkew rewrites an old edge's schema rejection of the
// exclusive-claim arguments into a version-skew diagnostic; other errors pass through.
func DescribeExclusiveClaimVersionSkew(tenant, environment, tool, remedy string, exclusive bool, err error) error {
	if err == nil || !exclusive {
		return err
	}
	names, ok := mcpUnexpectedAdditionalProperties(err)
	if !ok {
		return err
	}
	matched := false
	for _, name := range names {
		if exclusiveClaimVersionSkewArguments[name] {
			matched = true
			break
		}
	}
	if !matched {
		return err
	}
	return fmt.Errorf(
		"%s/%s's edge runs an erun release older than the one that added --exclusive to %s; %s\n"+
			"edge error: %w",
		tenant, environment, tool, remedy, err,
	)
}

// DescribeExclusiveActivityLeaseVersionSkew is DescribeExclusiveClaimVersionSkew for activity_lease_take.
func DescribeExclusiveActivityLeaseVersionSkew(tenant, environment string, exclusive bool, err error) error {
	return DescribeExclusiveClaimVersionSkew(tenant, environment, "activity_lease_take",
		"upgrade the environment (erun pin / erun deploy) to take an exclusive claim there, or omit --exclusive to take a plain presence lease",
		exclusive, err)
}
