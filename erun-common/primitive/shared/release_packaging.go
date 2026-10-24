package shared

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// requiredScoopBinaries are the executables the Windows Scoop install must put
// on PATH. erun-app.exe is load-bearing: `erun app`, Homebrew, and Scoop
// launcher wiring all depend on that artifact name.
var requiredScoopBinaries = []string{"erun.exe", "emcp.exe", "eapi.exe", "erun-app.exe"}

// Catches a drifted checked-in Scoop manifest before the release publishes it,
// rather than at a user's `scoop install`. Runs during release resolution so
// the bailout precedes any git mutation.
func validateStableScoopManifest(ctx core.Context, projectRoot string) error {
	scoopPath := filepath.Join(projectRoot, "bucket", "erun.json")
	if !FileExists(scoopPath) {
		return nil
	}
	ctx.Trace("release: validating scoop manifest " + scoopPath)
	return checkScoopManifestInvariants(scoopPath)
}

// Reports every manifest-invariant violation at once, so a single failed
// release names everything that needs fixing instead of stopping at the first.
func checkScoopManifestInvariants(scoopPath string) error {
	data, err := os.ReadFile(scoopPath)
	if err != nil {
		return err
	}
	var manifest struct {
		Depends   []string `json:"depends"`
		Installer struct {
			Script []string `json:"script"`
		} `json:"installer"`
		Bin []string `json:"bin"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("scoop manifest %s: %w", scoopPath, err)
	}

	var problems []string
	if !slices.Contains(manifest.Depends, "mingw") {
		problems = append(problems, `depends must include "mingw" for the Wails CGO build`)
	}
	if len(manifest.Installer.Script) == 0 {
		problems = append(problems, "installer.script must not be empty")
	}
	script := strings.Join(manifest.Installer.Script, "\n")
	if !strings.Contains(script, "MinGW for the Wails CGO build") {
		problems = append(problems, "installer.script must state the MinGW/Wails CGO prerequisite")
	}
	if strings.Contains(script, "Fyne") {
		problems = append(problems, "installer.script must not reference the stale Fyne prerequisite")
	}
	for _, bin := range requiredScoopBinaries {
		if !slices.Contains(manifest.Bin, bin) {
			problems = append(problems, "bin must include "+bin)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("scoop manifest %s is invalid: %s", scoopPath, strings.Join(problems, "; "))
	}
	return nil
}

func discoverStableReleasePackaging(projectRoot, version string) ([]ReleaseFileUpdate, *ReleasePackagingSyncSpec, error) {
	updates := make([]ReleaseFileUpdate, 0, 2)
	syncSpec := &ReleasePackagingSyncSpec{ProjectRoot: projectRoot, Version: version}

	formulaPath := filepath.Join(projectRoot, "Formula", "erun.rb")
	updates, err := appendReleasePackagingUpdate(updates, formulaPath, version, updateHomebrewFormulaReleaseVersion)
	if err != nil {
		return nil, nil, err
	}
	if FileExists(formulaPath) {
		syncSpec.FormulaPath = formulaPath
	}

	scoopPath := filepath.Join(projectRoot, "bucket", "erun.json")
	updates, err = appendReleasePackagingUpdate(updates, scoopPath, version, updateScoopManifestReleaseVersion)
	if err != nil {
		return nil, nil, err
	}
	if FileExists(scoopPath) {
		syncSpec.ScoopPath = scoopPath
	}

	marketplacePath := filepath.Join(projectRoot, ".claude-plugin", "marketplace.json")
	if FileExists(marketplacePath) {
		syncSpec.MarketplacePath = marketplacePath
	}

	if syncSpec.FormulaPath == "" && syncSpec.ScoopPath == "" && syncSpec.MarketplacePath == "" {
		return updates, nil, nil
	}

	return updates, syncSpec, nil
}

func appendReleasePackagingUpdate(
	updates []ReleaseFileUpdate,
	path, version string,
	update func(string, string) (string, bool, error),
) ([]ReleaseFileUpdate, error) {
	content, changed, err := update(path, version)
	if err != nil {
		return nil, err
	}
	if changed {
		updates = append(updates, ReleaseFileUpdate{
			Path:    path,
			Content: content,
		})
	}
	return updates, nil
}

func updateHomebrewFormulaReleaseVersion(formulaPath, version string) (string, bool, error) {
	data, err := os.ReadFile(formulaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	content := string(data)
	wantURL := `url "https://github.com/sophium/erun/archive/refs/tags/v` + version + `.tar.gz"`
	urlPattern := regexp.MustCompile(`(?m)^  url "https://github\.com/sophium/erun/archive/refs/tags/v[^"]+\.tar\.gz"$`)
	updated := urlPattern.ReplaceAllString(content, "  "+wantURL)
	if updated == content {
		return "", false, nil
	}

	return updated, true, nil
}

func updateScoopManifestReleaseVersion(manifestPath, version string) (string, bool, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	var manifest struct {
		Version     string   `json:"version"`
		Description string   `json:"description"`
		Homepage    string   `json:"homepage"`
		License     string   `json:"license"`
		Depends     []string `json:"depends,omitempty"`
		URL         string   `json:"url"`
		Hash        string   `json:"hash"`
		ExtractDir  string   `json:"extract_dir"`
		Installer   struct {
			Script []string `json:"script,omitempty"`
		} `json:"installer,omitempty"`
		Bin []string `json:"bin,omitempty"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", false, err
	}

	wantURL := "https://github.com/sophium/erun/archive/refs/tags/v" + version + ".zip"
	wantExtractDir := "erun-" + version
	if manifest.Version == version && manifest.URL == wantURL && manifest.ExtractDir == wantExtractDir {
		return "", false, nil
	}

	manifest.Version = version
	manifest.URL = wantURL
	manifest.ExtractDir = wantExtractDir

	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", false, err
	}

	return string(updated) + "\n", true, nil
}

func updateHomebrewFormulaReleaseChecksum(formulaPath, checksum string) (string, bool, error) {
	data, err := os.ReadFile(formulaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	content := string(data)
	want := `sha256 "` + checksum + `"`
	pattern := regexp.MustCompile(`(?m)^  sha256 "[0-9a-f]+"$`)
	updated := pattern.ReplaceAllString(content, "  "+want)
	if updated == content {
		return "", false, nil
	}

	return updated, true, nil
}

func updateScoopManifestReleaseChecksum(manifestPath, checksum string) (string, bool, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	var manifest struct {
		Version     string   `json:"version"`
		Description string   `json:"description"`
		Homepage    string   `json:"homepage"`
		License     string   `json:"license"`
		Depends     []string `json:"depends,omitempty"`
		URL         string   `json:"url"`
		Hash        string   `json:"hash"`
		ExtractDir  string   `json:"extract_dir"`
		Installer   struct {
			Script []string `json:"script,omitempty"`
		} `json:"installer,omitempty"`
		Bin []string `json:"bin,omitempty"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", false, err
	}
	if manifest.Hash == checksum {
		return "", false, nil
	}
	manifest.Hash = checksum
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", false, err
	}
	return string(updated) + "\n", true, nil
}

func syncReleasePackagingChecksums(ctx core.Context, spec ReleasePackagingSyncSpec) ([]ReleaseFileUpdate, error) {
	if spec.FormulaPath == "" && spec.ScoopPath == "" && spec.MarketplacePath == "" {
		return nil, nil
	}

	updates := make([]ReleaseFileUpdate, 0, 3)
	formulaUpdate, ok, err := syncHomebrewReleaseChecksum(ctx, spec)
	if err != nil {
		return nil, err
	}
	if ok {
		updates = append(updates, formulaUpdate)
	}
	scoopUpdate, ok, err := syncScoopReleaseChecksum(ctx, spec)
	if err != nil {
		return nil, err
	}
	if ok {
		updates = append(updates, scoopUpdate)
	}
	marketplaceUpdate, ok, err := SyncMarketplaceReleaseSHA(ctx, spec)
	if err != nil {
		return nil, err
	}
	if ok {
		updates = append(updates, marketplaceUpdate)
	}
	return updates, nil
}

func syncHomebrewReleaseChecksum(ctx core.Context, spec ReleasePackagingSyncSpec) (ReleaseFileUpdate, bool, error) {
	if spec.FormulaPath == "" {
		return ReleaseFileUpdate{}, false, nil
	}
	url := "https://github.com/sophium/erun/archive/refs/tags/v" + spec.Version + ".tar.gz"
	ctx.TraceCommand("", "curl", "-fsSL", url)
	ctx.TraceCommand("", "shasum", "-a", "256", "v"+spec.Version+".tar.gz")
	if ctx.DryRun {
		return ReleaseFileUpdate{}, false, nil
	}
	checksum, err := releaseArchiveSHA256(url)
	if err != nil {
		return ReleaseFileUpdate{}, false, err
	}
	content, changed, err := updateHomebrewFormulaReleaseChecksum(spec.FormulaPath, checksum)
	if err != nil || !changed {
		return ReleaseFileUpdate{}, false, err
	}
	return ReleaseFileUpdate{Path: spec.FormulaPath, Content: content}, true, nil
}

func syncScoopReleaseChecksum(ctx core.Context, spec ReleasePackagingSyncSpec) (ReleaseFileUpdate, bool, error) {
	if spec.ScoopPath == "" {
		return ReleaseFileUpdate{}, false, nil
	}
	url := "https://github.com/sophium/erun/archive/refs/tags/v" + spec.Version + ".zip"
	ctx.TraceCommand("", "curl", "-fsSL", url)
	ctx.TraceCommand("", "shasum", "-a", "256", "v"+spec.Version+".zip")
	if ctx.DryRun {
		return ReleaseFileUpdate{}, false, nil
	}
	checksum, err := releaseArchiveSHA256(url)
	if err != nil {
		return ReleaseFileUpdate{}, false, err
	}
	content, changed, err := updateScoopManifestReleaseChecksum(spec.ScoopPath, checksum)
	if err != nil || !changed {
		return ReleaseFileUpdate{}, false, err
	}
	return ReleaseFileUpdate{Path: spec.ScoopPath, Content: content}, true, nil
}

// Pins the marketplace catalogue's plugin sha to the release commit. Runs
// alongside Homebrew/Scoop because all three require the release tag to exist
// first.
func SyncMarketplaceReleaseSHA(ctx core.Context, spec ReleasePackagingSyncSpec) (ReleaseFileUpdate, bool, error) {
	if spec.MarketplacePath == "" {
		return ReleaseFileUpdate{}, false, nil
	}
	if spec.ProjectRoot == "" {
		return ReleaseFileUpdate{}, false, fmt.Errorf("release: marketplace sync requires ProjectRoot in sync spec")
	}
	ref := "v" + spec.Version + "^{}"
	ctx.TraceCommand("", "git", "-C", spec.ProjectRoot, "rev-parse", ref)
	if ctx.DryRun {
		return ReleaseFileUpdate{}, false, nil
	}
	output, err := core.Command("git", "-C", spec.ProjectRoot, "rev-parse", ref).CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(output)); detail != "" {
			return ReleaseFileUpdate{}, false, fmt.Errorf("resolve release tag %q: %w: %s", ref, err, detail)
		}
		return ReleaseFileUpdate{}, false, fmt.Errorf("resolve release tag %q: %w", ref, err)
	}
	sha := strings.TrimSpace(string(output))
	if sha == "" {
		return ReleaseFileUpdate{}, false, fmt.Errorf("resolve release tag %q: empty result", ref)
	}
	content, changed, err := updateMarketplaceReleaseSHA(spec.MarketplacePath, sha)
	if err != nil || !changed {
		return ReleaseFileUpdate{}, false, err
	}
	return ReleaseFileUpdate{Path: spec.MarketplacePath, Content: content}, true, nil
}

// Uses targeted text replacement rather than JSON marshal so the file's
// existing formatting and key ordering survive the sha bump.
func updateMarketplaceReleaseSHA(marketplacePath, sha string) (string, bool, error) {
	data, err := os.ReadFile(marketplacePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	content := string(data)
	pattern := regexp.MustCompile(`("sha":\s*")[0-9a-f]{7,40}(")`)
	updated := pattern.ReplaceAllString(content, `${1}`+sha+`${2}`)
	if updated == content {
		return "", false, nil
	}
	return updated, true, nil
}

func releaseArchiveSHA256(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		checksum, retry, err := fetchReleaseArchiveSHA256(client, url)
		if err == nil {
			return checksum, nil
		}
		lastErr = err
		if !retry {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("failed to download release archive %q", url)
	}
	return "", lastErr
}

func fetchReleaseArchiveSHA256(client *http.Client, url string) (string, bool, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", true, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
		return "", retry, fmt.Errorf("download %q failed: %s", url, resp.Status)
	}

	sum := sha256.New()
	if _, err := io.Copy(sum, resp.Body); err != nil {
		return "", true, err
	}
	return hex.EncodeToString(sum.Sum(nil)), false, nil
}
