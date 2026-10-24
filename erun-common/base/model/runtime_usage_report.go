package model

// The live reading is fed to RecommendRuntimeSizing with the retained history, so warnings
// and the recommendation come from one verdict rather than two engines.

// RuntimeUsageReport is a live reading plus the sizing recommendation `erun list` reports.
type RuntimeUsageReport struct {
	RuntimeUsage
	// Sizing is nil when nothing was observed or the history could not be read.
	Sizing *RuntimeSizingRecommendation `json:"sizing,omitempty"`
}
