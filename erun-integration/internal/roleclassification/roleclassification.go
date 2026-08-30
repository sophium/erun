// Package roleclassification implements the classifier behind
// TestRouteRoleClassificationGate: it fails when a registered, authenticated
// API route has no entry in erun-backend-api/internal/routeroles' Routes
// map, so adding a route forces a decision about which of the predefined
// roles (TenantUser, TenantAdmin, or neither) may call it, instead of a role
// silently gaining or missing it. Mirrors internal/desktopsurface's split: a
// pure classifier here, unit-tested against synthetic data; the wiring test
// at the module root supplies the real enumeration and real file reads.
package roleclassification

import "sort"

// Unclassified returns every route in routes with no entry in classified,
// sorted for a stable report. routes is the real registered protected-route
// catalog ("METHOD /path" keys, e.g. "GET /v1/reviews"); classified is the
// same key shape read from routeroles.Routes -- only a key's presence
// matters here, not which of TenantUserClass/TenantAdminOnly/OperationsOnly
// it maps to.
func Unclassified(routes []string, classified map[string]bool) []string {
	var missing []string
	for _, route := range routes {
		if !classified[route] {
			missing = append(missing, route)
		}
	}
	sort.Strings(missing)
	return missing
}
