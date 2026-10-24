package doctor

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/remoteinit"
)

// RemoteInitInspectionStatus categorizes one expected artifact's state.
type RemoteInitInspectionStatus string

const (
	RemoteInitInspectionStatusOK            RemoteInitInspectionStatus = "ok"
	RemoteInitInspectionStatusMissing       RemoteInitInspectionStatus = "missing"
	RemoteInitInspectionStatusNotApplicable RemoteInitInspectionStatus = "n/a"
)

// RemoteInitInspectionItem is one row in the inspection report.
type RemoteInitInspectionItem struct {
	Label  string
	Path   string
	Status RemoteInitInspectionStatus
}

// RemoteInitInspection is the doctor's view of remote-init state from
// inside the runtime pod: what the marker recorded and which local
// filesystem artifacts match that intent.
type RemoteInitInspection struct {
	MarkerPresent bool
	Marker        remoteinit.RemoteInitMarker

	// HomeDir, Tenant, Environment, ProjectRoot are the resolved values
	// doctor is operating against. When the marker is missing the fields
	// fall back to the runtime-pod env vars (ERUN_TENANT etc.) so the
	// report can still describe the target.
	HomeDir     string
	Tenant      string
	Environment string
	ProjectRoot string

	Items []RemoteInitInspectionItem
}

// Complete reports whether every applicable artifact matches the
// marker's intent. It is the signal doctor uses to decide whether to
// offer recovery.
func (r RemoteInitInspection) Complete() bool {
	if !r.MarkerPresent {
		return false
	}
	if !r.Marker.BootstrapComplete {
		return false
	}
	for _, item := range r.Items {
		if item.Status == RemoteInitInspectionStatusMissing {
			return false
		}
	}
	return true
}

// MissingItems returns only the items doctor would offer to finish.
func (r RemoteInitInspection) MissingItems() []RemoteInitInspectionItem {
	missing := make([]RemoteInitInspectionItem, 0, len(r.Items))
	for _, item := range r.Items {
		if item.Status == RemoteInitInspectionStatusMissing {
			missing = append(missing, item)
		}
	}
	return missing
}

// InspectRemoteInit reads the marker (if any) and probes the local
// filesystem for the artifacts init was expected to produce. The
// returned inspection is purely descriptive — no side effects.
func InspectRemoteInit(homeDir string, env func(string) string) (RemoteInitInspection, error) {
	if env == nil {
		env = os.Getenv
	}
	tenant := strings.TrimSpace(env("ERUN_TENANT"))
	environment := strings.TrimSpace(env("ERUN_ENVIRONMENT"))
	marker, present, err := remoteinit.LoadRemoteInitMarker(homeDir, tenant, environment)
	if err != nil {
		return RemoteInitInspection{}, err
	}
	inspection := RemoteInitInspection{
		MarkerPresent: present,
		Marker:        marker,
		HomeDir:       homeDir,
	}
	if present {
		inspection.Tenant = marker.Tenant
		inspection.Environment = marker.Environment
		inspection.ProjectRoot = marker.ProjectRoot
	}
	if inspection.Tenant == "" {
		inspection.Tenant = tenant
	}
	if inspection.Environment == "" {
		inspection.Environment = environment
	}
	if inspection.ProjectRoot == "" {
		inspection.ProjectRoot = strings.TrimSpace(env("ERUN_REPO_PATH"))
	}

	inspection.Items = append(inspection.Items, inspectProjectRoot(inspection.ProjectRoot))
	inspection.Items = append(inspection.Items, inspectGitCheckout(inspection.ProjectRoot, marker, present))
	inspection.Items = append(inspection.Items, inspectSSHKey(homeDir, marker, present))
	if item, ok := inspectCodeCommitSSHKey(homeDir, marker, present); ok {
		inspection.Items = append(inspection.Items, item)
	}
	if item, ok := inspectSkills(homeDir, env); ok {
		inspection.Items = append(inspection.Items, item)
	}
	return inspection, nil
}

func inspectProjectRoot(projectRoot string) RemoteInitInspectionItem {
	item := RemoteInitInspectionItem{
		Label: "project root",
		Path:  projectRoot,
	}
	if projectRoot == "" {
		item.Status = RemoteInitInspectionStatusMissing
		return item
	}
	info, err := os.Stat(projectRoot)
	if err != nil || !info.IsDir() {
		item.Status = RemoteInitInspectionStatusMissing
		return item
	}
	item.Status = RemoteInitInspectionStatusOK
	return item
}

func inspectGitCheckout(projectRoot string, marker remoteinit.RemoteInitMarker, markerPresent bool) RemoteInitInspectionItem {
	gitDir := ""
	if projectRoot != "" {
		gitDir = filepath.Join(projectRoot, ".git")
	}
	item := RemoteInitInspectionItem{
		Label: "git checkout",
		Path:  gitDir,
	}
	if markerPresent && marker.NoGit {
		item.Status = RemoteInitInspectionStatusNotApplicable
		return item
	}
	if projectRoot == "" {
		item.Status = RemoteInitInspectionStatusMissing
		return item
	}
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		item.Status = RemoteInitInspectionStatusOK
		return item
	}
	item.Status = RemoteInitInspectionStatusMissing
	return item
}

func inspectSSHKey(homeDir string, marker remoteinit.RemoteInitMarker, markerPresent bool) RemoteInitInspectionItem {
	keyPath := filepath.Join(homeDir, ".ssh", "id_ed25519")
	item := RemoteInitInspectionItem{
		Label: "SSH keypair",
		Path:  keyPath,
	}
	if markerPresent && marker.NoGit {
		item.Status = RemoteInitInspectionStatusNotApplicable
		return item
	}
	if _, err := os.Stat(keyPath); err == nil {
		item.Status = RemoteInitInspectionStatusOK
		return item
	}
	item.Status = RemoteInitInspectionStatusMissing
	return item
}

// inspectCodeCommitSSHKey reports the RSA codecommit keypair only when
// the marker explicitly records a CodeCommit host. When the marker is
// missing or did not record CodeCommit, the inspection omits this row
// — doctor falls back to detecting CodeCommit from the resolved URL
// inside RunRemoteInitFinish and generates the RSA key on the fly.
func inspectCodeCommitSSHKey(homeDir string, marker remoteinit.RemoteInitMarker, markerPresent bool) (RemoteInitInspectionItem, bool) {
	if !markerPresent || marker.NoGit || strings.TrimSpace(marker.CodeCommitHost) == "" {
		return RemoteInitInspectionItem{}, false
	}
	keyPath := defaultRemoteInitCodeCommitSSHKeyPath(homeDir)
	item := RemoteInitInspectionItem{
		Label: "CodeCommit SSH keypair",
		Path:  keyPath,
	}
	if _, err := os.Stat(keyPath); err == nil {
		item.Status = RemoteInitInspectionStatusOK
		return item, true
	}
	item.Status = RemoteInitInspectionStatusMissing
	return item, true
}

// bakedSkillsRoot is the runtime image's baked-in location for the canonical
// agent skills; ERUN_SKILLS_DIR overrides it in tests.
func bakedSkillsRoot(env func(string) string) string {
	if env != nil {
		if override := strings.TrimSpace(env("ERUN_SKILLS_DIR")); override != "" {
			return override
		}
	}
	return core.RuntimeBakedSkillsDir
}

// inspectSkills reports whether every baked agent skill is installed into
// ~/.claude/skills (where Claude looks for them). Returns ok=false — omitting
// the row — when the image has no baked skills, so the check only surfaces
// inside a runtime pod; a plain checkout sees no skills row.
func inspectSkills(homeDir string, env func(string) string) (RemoteInitInspectionItem, bool) {
	entries, err := os.ReadDir(bakedSkillsRoot(env))
	if err != nil {
		return RemoteInitInspectionItem{}, false
	}
	installedRoot := filepath.Join(homeDir, ".claude", "skills")
	item := RemoteInitInspectionItem{Label: "agent skills", Path: installedRoot}
	anyBaked := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		anyBaked = true
		if _, err := os.Stat(filepath.Join(installedRoot, entry.Name(), "SKILL.md")); err != nil {
			item.Status = RemoteInitInspectionStatusMissing
			return item, true
		}
	}
	if !anyBaked {
		return RemoteInitInspectionItem{}, false
	}
	item.Status = RemoteInitInspectionStatusOK
	return item, true
}
