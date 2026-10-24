package model

// LinuxPackageSpec is one linux package component. It never produces an image: a plain
// build runs its test stage; a release exports dist and uploads its root .deb files.
type LinuxPackageSpec struct {
	Name           string `json:"name"`
	ContextDir     string `json:"contextDir"`
	DockerfilePath string `json:"dockerfilePath"`
	// Fingerprint, Platform and Reuse are a plain build's reuse decision; a release never sets them.
	Fingerprint   string `json:"fingerprint,omitempty"`
	Platform      string `json:"platform,omitempty"`
	Reuse         bool   `json:"reuse,omitempty"`
	NoReuseReason string
}
