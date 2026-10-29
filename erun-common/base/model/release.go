package model

type ReleaseMode string

const (
	ReleaseModeStable     ReleaseMode = "stable"
	ReleaseModeCandidate  ReleaseMode = "candidate"
	ReleaseModePrerelease ReleaseMode = "prerelease"
)

type ReleaseFileUpdate struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ReleaseChartSpec struct {
	ChartPath  string `json:"chartPath"`
	Version    string `json:"version"`
	AppVersion string `json:"appVersion"`
}

type ReleaseDockerImageSpec struct {
	ContextDir     string `json:"contextDir"`
	DockerfilePath string `json:"dockerfilePath"`
	ImageName      string `json:"imageName"`
	Registry       string `json:"registry,omitempty"`
	Tag            string `json:"tag"`
	Version        string `json:"version"`
}

type ReleaseCommandSpec struct {
	Dir  string   `json:"dir,omitempty"`
	Name string   `json:"name"`
	Args []string `json:"args"`
}

type ReleaseStage struct {
	Name          string                    `json:"name"`
	FileUpdates   []ReleaseFileUpdate       `json:"fileUpdates,omitempty"`
	GitCommands   []ReleaseCommandSpec      `json:"gitCommands,omitempty"`
	PackagingSync *ReleasePackagingSyncSpec `json:"packagingSync,omitempty"`
}

type ReleasePackagingSyncSpec struct {
	ProjectRoot     string `json:"projectRoot,omitempty"`
	Version         string `json:"version"`
	FormulaPath     string `json:"formulaPath,omitempty"`
	ScoopPath       string `json:"scoopPath,omitempty"`
	MarketplacePath string `json:"marketplacePath,omitempty"`
}

// ReleaseSpec runs Stages while the release is still private and recoverable;
// PostPublishStages hold the irreversible pushes. `erun release` reaches them having
// published nothing, and its failures must not claim otherwise.
type ReleaseSpec struct {
	ProjectRoot       string                   `json:"projectRoot"`
	ReleaseRoot       string                   `json:"releaseRoot"`
	Branch            string                   `json:"branch"`
	Commit            string                   `json:"commit"`
	BaseVersion       string                   `json:"baseVersion"`
	Version           string                   `json:"version"`
	NextVersion       string                   `json:"nextVersion,omitempty"`
	VersionFilePath   string                   `json:"versionFilePath"`
	Mode              ReleaseMode              `json:"mode"`
	Force             bool                     `json:"force,omitempty"`
	Charts            []ReleaseChartSpec       `json:"charts,omitempty"`
	DockerImages      []ReleaseDockerImageSpec `json:"dockerImages,omitempty"`
	Stages            []ReleaseStage           `json:"stages,omitempty"`
	PostPublishStages []ReleaseStage           `json:"postPublishStages,omitempty"`
	// LinuxPackages are exported during the publish and attached once the tag is public.
	LinuxPackages   []LinuxPackageSpec `json:"linuxPackages,omitempty"`
	LinuxPackageDir string             `json:"linuxPackageDir,omitempty"`

	// ArtifactsPublished is run state, not plan; always false for `erun release`.
	ArtifactsPublished bool `json:"-"`
}
