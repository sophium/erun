package podruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Installing erun's skills and reusable agents into an assistant's discovery
// directory is shared by the runtime pod (from the image's baked copies) and the
// desktop's host orchestrator (from a checkout). Both install what is absent and
// refresh a copy erun wrote itself when the source changed, but never overwrite
// a copy the operator edited in place: a home directory outlives the image or
// build that first installed into it, so a one-time install would freeze every
// later update, and a blind overwrite would discard the operator's edits.
//
// Provenance is a marker holding the sha256 of the source erun last installed.
// An installed copy that still matches its marker is erun's and is refreshed; one
// that differs from both the source and its marker was edited and is kept. A
// copy with no marker predates markers and is treated as erun's.

const (
	// AgentSkillMarker sits inside an installed skill directory.
	AgentSkillMarker = ".erun-skill-baked-sha256"
	// AgentFileMarkerSuffix is appended to an installed agent file's name, since
	// an agent is one flat file with no directory to hold a marker.
	AgentFileMarkerSuffix = ".erun-agent-baked-sha256"
)

// AgentAssetsInstallResult counts what one install pass did, per outcome.
type AgentAssetsInstallResult struct {
	Installed []string `json:"installed"`
	Refreshed []string `json:"refreshed"`
	Current   []string `json:"current"`
	Preserved []string `json:"preserved"`
}

type agentAssetOutcome int

const (
	agentAssetSkipped agentAssetOutcome = iota
	agentAssetInstalled
	agentAssetRefreshed
	agentAssetCurrent
	agentAssetPreserved
)

func (r *AgentAssetsInstallResult) record(name string, outcome agentAssetOutcome) {
	switch outcome {
	case agentAssetInstalled:
		r.Installed = append(r.Installed, name)
	case agentAssetRefreshed:
		r.Refreshed = append(r.Refreshed, name)
	case agentAssetCurrent:
		r.Current = append(r.Current, name)
	case agentAssetPreserved:
		r.Preserved = append(r.Preserved, name)
	}
}

// InstallOrRefreshSkills reconciles every skill directory under srcRoot (one
// holding a SKILL.md) into destRoot/<name>.
func InstallOrRefreshSkills(srcRoot, destRoot string) (AgentAssetsInstallResult, error) {
	var result AgentAssetsInstallResult
	entries, err := os.ReadDir(srcRoot)
	if err != nil {
		return result, fmt.Errorf("read skills source %s: %w", srcRoot, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		outcome, err := installOrRefreshSkill(filepath.Join(srcRoot, entry.Name()), filepath.Join(destRoot, entry.Name()))
		if err != nil {
			return result, fmt.Errorf("install skill %s: %w", entry.Name(), err)
		}
		result.record(entry.Name(), outcome)
	}
	return result, nil
}

// InstallOrRefreshAgents reconciles every <name>.md under srcRoot into
// destRoot/<name>.md.
func InstallOrRefreshAgents(srcRoot, destRoot string) (AgentAssetsInstallResult, error) {
	var result AgentAssetsInstallResult
	entries, err := os.ReadDir(srcRoot)
	if err != nil {
		return result, fmt.Errorf("read agents source %s: %w", srcRoot, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		outcome, err := installOrRefreshAgent(filepath.Join(srcRoot, entry.Name()), filepath.Join(destRoot, entry.Name()))
		if err != nil {
			return result, fmt.Errorf("install agent %s: %w", entry.Name(), err)
		}
		result.record(entry.Name(), outcome)
	}
	return result, nil
}

func installOrRefreshSkill(src, dst string) (agentAssetOutcome, error) {
	sourceMD := filepath.Join(src, "SKILL.md")
	if _, err := os.Stat(sourceMD); err != nil {
		return agentAssetSkipped, nil // a directory with no SKILL.md is not a skill
	}
	installedMD := filepath.Join(dst, "SKILL.md")
	if _, err := os.Stat(installedMD); err != nil {
		return agentAssetInstalled, copySkill(src, dst)
	}
	outcome, err := reconcileInstalledAgentAsset(sourceMD, installedMD, filepath.Join(dst, AgentSkillMarker))
	if err != nil || outcome != agentAssetRefreshed {
		return outcome, err
	}
	return outcome, copySkill(src, dst)
}

func installOrRefreshAgent(src, dst string) (agentAssetOutcome, error) {
	if _, err := os.Stat(dst); err != nil {
		return agentAssetInstalled, copyAgent(src, dst)
	}
	outcome, err := reconcileInstalledAgentAsset(src, dst, dst+AgentFileMarkerSuffix)
	if err != nil || outcome != agentAssetRefreshed {
		return outcome, err
	}
	return outcome, copyAgent(src, dst)
}

// reconcileInstalledAgentAsset decides what an existing install needs. A copy
// already matching the source only has its marker (re)written, which also
// adopts a pre-marker copy that happens to match, so a later change refreshes it.
func reconcileInstalledAgentAsset(sourceFile, installedFile, marker string) (agentAssetOutcome, error) {
	sourceSHA, err := FileSHA256(sourceFile)
	if err != nil {
		return agentAssetSkipped, err
	}
	installedSHA, err := FileSHA256(installedFile)
	if err != nil {
		return agentAssetSkipped, err
	}
	if installedSHA == sourceSHA {
		return agentAssetCurrent, os.WriteFile(marker, []byte(sourceSHA+"\n"), 0o644)
	}
	markerSHA := ""
	if data, readErr := os.ReadFile(marker); readErr == nil {
		markerSHA = strings.TrimSpace(string(data))
	}
	if markerSHA == "" || installedSHA == markerSHA {
		return agentAssetRefreshed, nil
	}
	return agentAssetPreserved, nil
}

func copySkill(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := copyAgentAssetTree(src, dst); err != nil {
		return err
	}
	sha, err := FileSHA256(filepath.Join(src, "SKILL.md"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, AgentSkillMarker), []byte(sha+"\n"), 0o644)
}

func copyAgent(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	// WriteFile keeps an existing file's mode; an installed copy is always 0644.
	if err := os.Chmod(dst, 0o644); err != nil {
		return err
	}
	sha, err := FileSHA256(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst+AgentFileMarkerSuffix, []byte(sha+"\n"), 0o644)
}

// copyAgentAssetTree copies src into dst with directories 0755 and files 0644,
// whatever modes the source carries (the image bakes skills 0755 so the erun
// user can enter them).
func copyAgentAssetTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// FileSHA256 returns the hex sha256 of a file's contents.
func FileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
