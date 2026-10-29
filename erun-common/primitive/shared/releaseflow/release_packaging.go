package releaseflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

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

func syncReleasePackagingChecksums(ctx core.Context, spec model.ReleasePackagingSyncSpec) ([]model.ReleaseFileUpdate, error) {
	if spec.FormulaPath == "" && spec.ScoopPath == "" && spec.MarketplacePath == "" {
		return nil, nil
	}

	updates := make([]model.ReleaseFileUpdate, 0, 3)
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

func syncHomebrewReleaseChecksum(ctx core.Context, spec model.ReleasePackagingSyncSpec) (model.ReleaseFileUpdate, bool, error) {
	if spec.FormulaPath == "" {
		return model.ReleaseFileUpdate{}, false, nil
	}
	url := "https://github.com/sophium/erun/archive/refs/tags/v" + spec.Version + ".tar.gz"
	ctx.TraceCommand("", "curl", "-fsSL", url)
	ctx.TraceCommand("", "shasum", "-a", "256", "v"+spec.Version+".tar.gz")
	if ctx.DryRun {
		return model.ReleaseFileUpdate{}, false, nil
	}
	checksum, err := releaseArchiveSHA256(url)
	if err != nil {
		return model.ReleaseFileUpdate{}, false, err
	}
	content, changed, err := updateHomebrewFormulaReleaseChecksum(spec.FormulaPath, checksum)
	if err != nil || !changed {
		return model.ReleaseFileUpdate{}, false, err
	}
	return model.ReleaseFileUpdate{Path: spec.FormulaPath, Content: content}, true, nil
}

func syncScoopReleaseChecksum(ctx core.Context, spec model.ReleasePackagingSyncSpec) (model.ReleaseFileUpdate, bool, error) {
	if spec.ScoopPath == "" {
		return model.ReleaseFileUpdate{}, false, nil
	}
	url := "https://github.com/sophium/erun/archive/refs/tags/v" + spec.Version + ".zip"
	ctx.TraceCommand("", "curl", "-fsSL", url)
	ctx.TraceCommand("", "shasum", "-a", "256", "v"+spec.Version+".zip")
	if ctx.DryRun {
		return model.ReleaseFileUpdate{}, false, nil
	}
	checksum, err := releaseArchiveSHA256(url)
	if err != nil {
		return model.ReleaseFileUpdate{}, false, err
	}
	content, changed, err := updateScoopManifestReleaseChecksum(spec.ScoopPath, checksum)
	if err != nil || !changed {
		return model.ReleaseFileUpdate{}, false, err
	}
	return model.ReleaseFileUpdate{Path: spec.ScoopPath, Content: content}, true, nil
}

// Needs the release tag to exist, like Homebrew and Scoop.
func SyncMarketplaceReleaseSHA(ctx core.Context, spec model.ReleasePackagingSyncSpec) (model.ReleaseFileUpdate, bool, error) {
	if spec.MarketplacePath == "" {
		return model.ReleaseFileUpdate{}, false, nil
	}
	if spec.ProjectRoot == "" {
		return model.ReleaseFileUpdate{}, false, fmt.Errorf("release: marketplace sync requires ProjectRoot in sync spec")
	}
	ref := "v" + spec.Version + "^{}"
	ctx.TraceCommand("", "git", "-C", spec.ProjectRoot, "rev-parse", ref)
	if ctx.DryRun {
		return model.ReleaseFileUpdate{}, false, nil
	}
	output, err := core.Command("git", "-C", spec.ProjectRoot, "rev-parse", ref).CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(output)); detail != "" {
			return model.ReleaseFileUpdate{}, false, fmt.Errorf("resolve release tag %q: %w: %s", ref, err, detail)
		}
		return model.ReleaseFileUpdate{}, false, fmt.Errorf("resolve release tag %q: %w", ref, err)
	}
	sha := strings.TrimSpace(string(output))
	if sha == "" {
		return model.ReleaseFileUpdate{}, false, fmt.Errorf("resolve release tag %q: empty result", ref)
	}
	content, changed, err := updateMarketplaceReleaseSHA(spec.MarketplacePath, sha)
	if err != nil || !changed {
		return model.ReleaseFileUpdate{}, false, err
	}
	return model.ReleaseFileUpdate{Path: spec.MarketplacePath, Content: content}, true, nil
}

// Text replacement keeps the file's formatting and key order.
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
