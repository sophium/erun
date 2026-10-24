package backendapi

import (
	"sort"

	"github.com/sophium/erun/erun-common/base/model"
)

// routeCatalog is every protected route this handler registered, in the same
// canonical (method, path template) form authorization and auditing key off.
// It is the candidate set the capability answer is resolved over, so a client
// is only ever told about surfaces that exist.
type routeCatalog struct {
	entries []model.PlatformCapability
}

func (c *routeCatalog) add(method string, apiPath string) {
	c.entries = append(c.entries, model.PlatformCapability{Method: method, Path: apiPath})
}

// sorted returns the catalog ordered by path then method, so a capability
// response is stable across restarts and comparable between callers.
func (c *routeCatalog) sorted() []model.PlatformCapability {
	sorted := make([]model.PlatformCapability, len(c.entries))
	copy(sorted, c.entries)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Path != sorted[j].Path {
			return sorted[i].Path < sorted[j].Path
		}
		return sorted[i].Method < sorted[j].Method
	})
	return sorted
}
