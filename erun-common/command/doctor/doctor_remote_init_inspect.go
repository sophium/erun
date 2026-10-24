package doctor

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/remoteinit"
)

// InspectRemoteInit reads the marker and probes the filesystem, without side effects.
func InspectRemoteInit(homeDir string, env func(string) string) (model.RemoteInitInspection, error) {
	if env == nil {
		env = os.Getenv
	}
	tenant := strings.TrimSpace(env("ERUN_TENANT"))
	environment := strings.TrimSpace(env("ERUN_ENVIRONMENT"))
	marker, present, err := remoteinit.LoadRemoteInitMarker(homeDir, tenant, environment)
	if err != nil {
		return model.RemoteInitInspection{}, err
	}
	inspection := model.RemoteInitInspection{
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

func inspectProjectRoot(projectRoot string) model.RemoteInitInspectionItem {
	item := model.RemoteInitInspectionItem{
		Label: "project root",
		Path:  projectRoot,
	}
	if projectRoot == "" {
		item.Status = model.RemoteInitInspectionStatusMissing
		return item
	}
	info, err := os.Stat(projectRoot)
	if err != nil || !info.IsDir() {
		item.Status = model.RemoteInitInspectionStatusMissing
		return item
	}
	item.Status = model.RemoteInitInspectionStatusOK
	return item
}

func inspectGitCheckout(projectRoot string, marker model.RemoteInitMarker, markerPresent bool) model.RemoteInitInspectionItem {
	gitDir := ""
	if projectRoot != "" {
		gitDir = filepath.Join(projectRoot, ".git")
	}
	item := model.RemoteInitInspectionItem{
		Label: "git checkout",
		Path:  gitDir,
	}
	if markerPresent && marker.NoGit {
		item.Status = model.RemoteInitInspectionStatusNotApplicable
		return item
	}
	if projectRoot == "" {
		item.Status = model.RemoteInitInspectionStatusMissing
		return item
	}
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		item.Status = model.RemoteInitInspectionStatusOK
		return item
	}
	item.Status = model.RemoteInitInspectionStatusMissing
	return item
}

func inspectSSHKey(homeDir string, marker model.RemoteInitMarker, markerPresent bool) model.RemoteInitInspectionItem {
	keyPath := filepath.Join(homeDir, ".ssh", "id_ed25519")
	item := model.RemoteInitInspectionItem{
		Label: "SSH keypair",
		Path:  keyPath,
	}
	if markerPresent && marker.NoGit {
		item.Status = model.RemoteInitInspectionStatusNotApplicable
		return item
	}
	if _, err := os.Stat(keyPath); err == nil {
		item.Status = model.RemoteInitInspectionStatusOK
		return item
	}
	item.Status = model.RemoteInitInspectionStatusMissing
	return item
}

// inspectCodeCommitSSHKey only reports when the marker records CodeCommit;
// otherwise RunRemoteInitFinish detects it from the URL.
func inspectCodeCommitSSHKey(homeDir string, marker model.RemoteInitMarker, markerPresent bool) (model.RemoteInitInspectionItem, bool) {
	if !markerPresent || marker.NoGit || strings.TrimSpace(marker.CodeCommitHost) == "" {
		return model.RemoteInitInspectionItem{}, false
	}
	keyPath := defaultRemoteInitCodeCommitSSHKeyPath(homeDir)
	item := model.RemoteInitInspectionItem{
		Label: "CodeCommit SSH keypair",
		Path:  keyPath,
	}
	if _, err := os.Stat(keyPath); err == nil {
		item.Status = model.RemoteInitInspectionStatusOK
		return item, true
	}
	item.Status = model.RemoteInitInspectionStatusMissing
	return item, true
}

// bakedSkillsRoot is overridden by ERUN_SKILLS_DIR in tests.
func bakedSkillsRoot(env func(string) string) string {
	if env != nil {
		if override := strings.TrimSpace(env("ERUN_SKILLS_DIR")); override != "" {
			return override
		}
	}
	return core.RuntimeBakedSkillsDir
}

// inspectSkills reports whether baked skills are installed in ~/.claude/skills;
// ok is false when the image bakes none.
func inspectSkills(homeDir string, env func(string) string) (model.RemoteInitInspectionItem, bool) {
	entries, err := os.ReadDir(bakedSkillsRoot(env))
	if err != nil {
		return model.RemoteInitInspectionItem{}, false
	}
	installedRoot := filepath.Join(homeDir, ".claude", "skills")
	item := model.RemoteInitInspectionItem{Label: "agent skills", Path: installedRoot}
	anyBaked := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		anyBaked = true
		if _, err := os.Stat(filepath.Join(installedRoot, entry.Name(), "SKILL.md")); err != nil {
			item.Status = model.RemoteInitInspectionStatusMissing
			return item, true
		}
	}
	if !anyBaked {
		return model.RemoteInitInspectionItem{}, false
	}
	item.Status = model.RemoteInitInspectionStatusOK
	return item, true
}
