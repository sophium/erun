package backendapi

import (
	"sort"

	"github.com/sophium/erun/erun-common/base/model"
)

// routeCatalog is every registered protected route, so capabilities name only real surfaces.
type routeCatalog struct {
	entries []model.PlatformCapability
}

func (c *routeCatalog) add(method string, apiPath string) {
	c.entries = append(c.entries, model.PlatformCapability{Method: method, Path: apiPath})
}

// sorted orders by path then method so capability responses are stable.
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
