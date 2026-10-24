package platformclient

import (
	"strings"
)

// PlatformAccessRemedy is the grant-role command and role name that would cure a capability denial.
type PlatformAccessRemedy struct {
	// Command is a complete, pasteable grant-role line.
	Command  string `json:"command,omitempty"`
	RoleName string `json:"roleName,omitempty"`
}

// PlatformAccessRemedyFor matches roles by Covers, never by name, so the command
// really grants the access; the first covering role in platform order wins.
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

// capabilityReadMethods are the methods a permission may name.
var capabilityReadMethods = map[string]bool{
	"GET": true, "HEAD": true, "OPTIONS": true,
	"POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// SplitCapabilityRead splits a route template ("GET /v1/x") into method and path.
// It rejects prose of the same shape ("the reviewer role").
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
