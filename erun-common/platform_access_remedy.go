package eruncommon

import "strings"

// PlatformAccessRemedy is what a capability denial hands the operator instead
// of stopping at the name of what they lack: the command an administrator runs
// to grant the caller a role that carries it, plus the name of that role so
// the request can be made in the platform's own vocabulary.
//
// It exists because the two halves have to agree. A denial that only names the
// missing role leaves the operator to find out which command grants it and to
// reassemble the ids by hand, which is the work the enrolment hand-off already
// removes for the not-enrolled case; a command assembled without checking a
// role actually covers the access would be worse, since a pasted command that
// grants nothing still reads as success.
type PlatformAccessRemedy struct {
	// Command is the full, copyable `erun platform user grant-role` line, with
	// the caller's own user id already filled in. Every value in it is real:
	// the operator pastes it rather than completing it.
	Command string `json:"command,omitempty"`
	// RoleName is the role Command grants, in the platform's vocabulary, so a
	// sentence around it can name what to ask for.
	RoleName string `json:"roleName,omitempty"`
}

// PlatformAccessRemedyFor renders the grant that would give userID the access
// they were refused for method on apiPath, or reports false when no role in
// roles covers it.
//
// It resolves the role by the same exact-or-pattern rule the platform's own
// capability resolution uses (PlatformRole.Covers) rather than by name: a
// role's name says nothing about what it grants, so naming a role from the
// missing access is the only way to promise access that the command will
// actually deliver. When several roles cover it, the first in the order the
// platform returned is used — any of them grants the access, and inventing a
// preference among them (fewest permissions, say) would be a guess this layer
// cannot back up.
func PlatformAccessRemedyFor(userID string, roles []PlatformRole, method string, apiPath string) (PlatformAccessRemedy, bool) {
	userID = strings.TrimSpace(userID)
	method = strings.TrimSpace(method)
	apiPath = strings.TrimSpace(apiPath)
	if userID == "" || method == "" || apiPath == "" {
		return PlatformAccessRemedy{}, false
	}
	for _, role := range roles {
		if !role.Covers(method, apiPath) {
			continue
		}
		roleID := strings.TrimSpace(role.RoleID)
		if roleID == "" {
			continue
		}
		return PlatformAccessRemedy{
			Command:  "erun platform user grant-role --user-id " + userID + " --role-id " + roleID,
			RoleName: strings.TrimSpace(role.Name),
		}, true
	}
	return PlatformAccessRemedy{}, false
}

// capabilityReadMethods are the methods a permission may name, and so the only
// ones a restricted-access value can begin with.
var capabilityReadMethods = map[string]bool{
	"GET": true, "HEAD": true, "OPTIONS": true,
	"POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// SplitCapabilityRead splits one of the platform's canonical route templates
// ("GET /v1/reviews/{review_id}") into its method and path, reporting false
// when the value is not one.
//
// The check is deliberately more than "two words": a restricted-access value
// is a route template, but a denial phrased in prose ("the reviewer role") has
// the same shape and would otherwise be resolved against an access nobody
// has — naming a role as if it were the thing that was refused.
func SplitCapabilityRead(read string) (method string, apiPath string, ok bool) {
	method, apiPath, found := strings.Cut(strings.TrimSpace(read), " ")
	if !found {
		return "", "", false
	}
	method = strings.TrimSpace(method)
	apiPath = strings.TrimSpace(apiPath)
	if !capabilityReadMethods[method] || !strings.HasPrefix(apiPath, "/") {
		return "", "", false
	}
	return method, apiPath, true
}
