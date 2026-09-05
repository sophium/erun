package eruncommon

// PlatformCapability is one API surface the caller may reach: a canonical route
// template such as `/v1/reviews/{review_id}`, never a concrete request URL.
type PlatformCapability struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// PlatformCapabilities is the caller's effective permission set as
// erun-backend-api resolved it: every registered route the server would let
// this caller through to, already expanded from the exact and pattern rules
// their roles carry. It is advisory — a surface renders from it so it can
// degrade instead of failing after the click — and the server's own
// per-request check remains the only authority.
//
// Never derive permission from role names instead. A role's name says nothing
// about what a tenant granted it, so a surface gated on the name is wrong for
// every custom role and wrong again the moment a role's permissions change.
type PlatformCapabilities []PlatformCapability

// Known reports whether the caller learned its capability set at all. An
// unknown set is not an empty one: a client that could not learn the answer
// must attempt the call and report what the server says, rather than hide a
// surface the caller may in fact be able to use. This is why the API serializes
// an empty set as `[]` and an unavailable one as `null`.
func (c PlatformCapabilities) Known() bool {
	return c != nil
}

// Allows reports whether the caller may call method on the canonical apiPath.
func (c PlatformCapabilities) Allows(method string, apiPath string) bool {
	for _, capability := range c {
		if capability.Method == method && capability.Path == apiPath {
			return true
		}
	}
	return false
}
