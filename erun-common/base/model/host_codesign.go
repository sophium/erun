package model

// HostArtifactSigning is the signing outcome for one artifact; Identity is empty for ad-hoc.
type HostArtifactSigning struct {
	Path     string `json:"path"`
	Signed   bool   `json:"signed"`
	Identity string `json:"identity,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Describe returns the operator-facing line, or "" when nothing is worth reporting.
func (s HostArtifactSigning) Describe() string {
	if s.Note != "" {
		return s.Note
	}
	if !s.Signed {
		return ""
	}
	if s.Identity != "" {
		return "Signed " + s.Path + " as " + s.Identity + " so macOS will run it and its privacy grants survive a rebuild"
	}
	return "Ad-hoc signed " + s.Path + " so macOS will run it"
}
