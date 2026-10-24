// Package roleclassification flags authenticated API routes missing from
// routeroles.Routes, so each new route forces a role decision.
package roleclassification

import "sort"

// Unclassified returns the routes ("METHOD /path") absent from classified,
// sorted.
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
