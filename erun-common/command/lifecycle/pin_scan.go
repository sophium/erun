package lifecycle

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Vendored charts/ trees are skipped: `helm dependency update` regenerates them.
func scanPinFiles(projectRoot, target string) ([]PinSite, error) {
	var sites []PinSite
	walkErr := filepath.WalkDir(projectRoot, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() && skipPinScanDir(entry.Name()) {
			return filepath.SkipDir
		}
		if err == nil && !entry.IsDir() {
			sites = append(sites, readablePinSites(projectRoot, path, entry.Name(), target)...)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.SliceStable(sites, func(i, j int) bool {
		if sites[i].Path == sites[j].Path {
			return sites[i].Detail < sites[j].Detail
		}
		return sites[i].Path < sites[j].Path
	})
	return sites, nil
}

func readablePinSites(projectRoot, path, name, target string) []PinSite {
	relative, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return nil
	}
	found, err := pinSitesInFile(path, filepath.ToSlash(relative), name, target)
	if err != nil {
		return nil
	}
	return found
}

func skipPinScanDir(name string) bool {
	switch name {
	case ".git", "node_modules", "charts", ".terraform", "vendor", "build", "dist":
		return true
	}
	return false
}

func pinSitesInFile(path, relative, name, target string) ([]PinSite, error) {
	if !pinScannableFile(name) {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)

	var sites []PinSite
	for _, match := range erunTerraformRefPattern.FindAllStringSubmatch(content, -1) {
		sites = append(sites, PinSite{Kind: PinSiteTerraformRef, Path: relative, Detail: match[0], Current: match[2], Target: target})
	}
	for _, match := range erunHelmDependencyPattern.FindAllStringSubmatch(content, -1) {
		sites = append(sites, PinSite{Kind: PinSiteHelmDependency, Path: relative, Detail: match[2], Current: match[3], Target: target})
	}
	for _, match := range erunDevopsImagePattern.FindAllStringSubmatch(content, -1) {
		sites = append(sites, PinSite{Kind: PinSiteRuntimeImage, Path: relative, Detail: "erun-devops", Current: match[2], Target: target})
	}
	if pinTerraformFile(name) {
		for _, match := range erunImageReferencePattern.FindAllStringSubmatch(content, -1) {
			if match[3] == "erun-devops" {
				continue
			}
			sites = append(sites, PinSite{Kind: PinSiteImageReference, Path: relative, Detail: match[3], Current: match[4], Target: target})
		}
	}
	return sites, nil
}

func pinScannableFile(name string) bool {
	switch {
	case pinTerraformFile(name):
		return true
	case name == "Chart.yaml":
		return true
	case name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile."):
		return true
	}
	return false
}

func pinTerraformFile(name string) bool {
	return strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tfvars")
}
