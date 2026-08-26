package eruncommon

import (
	"fmt"
	"regexp"
	"strings"
)

// The exclusive-claim take added exclusive/orchestrator to
// activity_lease_take. An environment's MCP edge compiles its tool schema
// from whatever erun release it runs, so an edge older than that release
// rejects those two properties as unknown -- the go-sdk's own JSON-schema
// validator answers before the request ever reaches the tool handler, with a
// raw "unexpected additional properties" message that reads exactly like a
// caller's malformed call, not like a version mismatch. This file closes
// that diagnostic gap.

// mcpUnexpectedAdditionalPropertiesPattern matches the go-sdk's schema
// validation phrasing for arguments the server's compiled-in tool schema does
// not know: `unexpected additional properties ["name" "name"]`.
var mcpUnexpectedAdditionalPropertiesPattern = regexp.MustCompile(`unexpected additional properties \[([^\]]*)\]`)

// mcpUnexpectedAdditionalProperties extracts the property names an MCP edge
// rejected as unknown from a schema-validation failure, or reports false for
// any other error shape -- including a genuinely malformed call, which must
// never be reframed as a version mismatch it is not.
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

// exclusiveActivityLeaseVersionSkewArguments are the exclusive-claim
// properties an edge older than the release that added them does not know.
var exclusiveActivityLeaseVersionSkewArguments = map[string]bool{
	"exclusive":    true,
	"orchestrator": true,
}

// DescribeExclusiveActivityLeaseVersionSkew recognises the one raw failure
// shape an edge older than the release that added --exclusive produces for
// an exclusive lease take --
// its compiled-in schema rejecting "exclusive"/"orchestrator" as unexpected --
// and turns it into a diagnostic naming the actual cause (this environment's
// edge predates the verb, not a malformed call) and the remedy. Any other
// failure passes through unchanged: an unreachable edge, an auth failure, or a
// genuinely malformed call must not be reframed as a version mismatch it
// is not.
func DescribeExclusiveActivityLeaseVersionSkew(tenant, environment string, exclusive bool, err error) error {
	if err == nil || !exclusive {
		return err
	}
	names, ok := mcpUnexpectedAdditionalProperties(err)
	if !ok {
		return err
	}
	matched := false
	for _, name := range names {
		if exclusiveActivityLeaseVersionSkewArguments[name] {
			matched = true
			break
		}
	}
	if !matched {
		return err
	}
	return fmt.Errorf(
		"%s/%s's edge runs an erun release older than the one that added --exclusive to activity_lease_take; "+
			"upgrade the environment (erun pin / erun deploy) to take an exclusive claim there, or omit --exclusive to take a plain presence lease\n"+
			"edge error: %w",
		tenant, environment, err,
	)
}
