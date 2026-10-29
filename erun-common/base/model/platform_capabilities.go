package model

// PlatformCapability is a canonical route template, never a concrete URL.
type PlatformCapability struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// PlatformCapabilities is the caller's advisory permission set from
// erun-backend-api; the server's per-request check is authoritative. Never gate
// on role names: custom roles make them meaningless.
type PlatformCapabilities []PlatformCapability

// Known reports whether the set was learned at all: an unknown set (null) is not
// empty, and the caller must attempt the call rather than hide the surface.
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
