package uimodel

// ChartPlan is the runtime chart a deploy at the chosen version would
// install; the picked version may name an image with no chart at that version.
type ChartPlan struct {
	Reference string `json:"reference"`
	Version   string `json:"version"`
	Chart     string `json:"chart"`
	// Source is "stated", "tenant", "canonical" or "local".
	Source string `json:"source"`
	// Missing means the registry positively answered "no chart"; an unreadable
	// registry sets Unknown instead, so a guess never blocks a deploy.
	Missing bool `json:"missing"`
	Unknown bool `json:"unknown"`
}

// VersionNotice explains empty version suggestions; Kind is "auth" or "unreachable".
type VersionNotice struct {
	Image string `json:"image"`
	Kind  string `json:"kind"`
}
