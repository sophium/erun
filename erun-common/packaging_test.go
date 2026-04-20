package eruncommon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPackageManagerDefinitionsTrackLatestStableTag(t *testing.T) {
	repoRoot := repoRootForPackagingTest(t)
	latestTag := latestStableReleaseTag(t, repoRoot)
	latestVersion := strings.TrimPrefix(latestTag, "v")

	formulaPath := filepath.Join(repoRoot, "Formula", "erun.rb")
	formulaData, err := os.ReadFile(formulaPath)
	if err != nil {
		t.Fatalf("read formula: %v", err)
	}
	formula := string(formulaData)
	if !strings.Contains(formula, `url "https://github.com/sophium/erun/archive/refs/tags/`+latestTag+`.tar.gz"`) {
		t.Fatalf("formula does not target latest stable tag %s:\n%s", latestTag, formula)
	}
	if !strings.Contains(formula, `bin/"erun"`) || !strings.Contains(formula, `bin/"emcp"`) {
		t.Fatalf("formula does not build both executables:\n%s", formula)
	}
	formulaSHA := regexp.MustCompile(`(?m)^  sha256 "([0-9a-f]+)"$`).FindStringSubmatch(formula)
	if len(formulaSHA) != 2 {
		t.Fatalf("formula sha256 not found:\n%s", formula)
	}
	gotFormulaSHA := releaseArchiveSHA256ForTest(t, "https://github.com/sophium/erun/archive/refs/tags/"+latestTag+".tar.gz")
	if formulaSHA[1] != gotFormulaSHA {
		t.Fatalf("formula sha256 = %q, want %q", formulaSHA[1], gotFormulaSHA)
	}

	scoopPath := filepath.Join(repoRoot, "bucket", "erun.json")
	scoopData, err := os.ReadFile(scoopPath)
	if err != nil {
		t.Fatalf("read scoop manifest: %v", err)
	}

	var manifest struct {
		Version    string `json:"version"`
		URL        string `json:"url"`
		Hash       string `json:"hash"`
		ExtractDir string `json:"extract_dir"`
		Depends    []string
		Bin        []string
		Installer  struct {
			Script []string `json:"script"`
		} `json:"installer"`
	}
	if err := json.Unmarshal(scoopData, &manifest); err != nil {
		t.Fatalf("unmarshal scoop manifest: %v", err)
	}

	if manifest.Version != latestVersion {
		t.Fatalf("scoop version = %q, want %q", manifest.Version, latestVersion)
	}
	wantZipURL := "https://github.com/sophium/erun/archive/refs/tags/" + latestTag + ".zip"
	if manifest.URL != wantZipURL {
		t.Fatalf("scoop url = %q, want %q", manifest.URL, wantZipURL)
	}
	gotScoopSHA := releaseArchiveSHA256ForTest(t, manifest.URL)
	if manifest.Hash != gotScoopSHA {
		t.Fatalf("scoop hash = %q, want %q", manifest.Hash, gotScoopSHA)
	}
	if manifest.ExtractDir != "erun-"+latestVersion {
		t.Fatalf("scoop extract_dir = %q, want %q", manifest.ExtractDir, "erun-"+latestVersion)
	}
		if !containsString(manifest.Depends, "go") {
			t.Fatalf("scoop manifest missing go dependency: %+v", manifest.Depends)
		}
		if !containsString(manifest.Depends, "mingw") {
			t.Fatalf("scoop manifest missing mingw dependency for erun-app.exe: %+v", manifest.Depends)
		}
	if !containsString(manifest.Bin, "erun.exe") || !containsString(manifest.Bin, "emcp.exe") {
		t.Fatalf("scoop manifest does not shim both executables: %+v", manifest.Bin)
	}
	script := strings.Join(manifest.Installer.Script, "\n")
	if !strings.Contains(script, `go build -trimpath -ldflags $cliLdflags -o "$dir\erun.exe" .`) {
		t.Fatalf("scoop installer does not build erun.exe:\n%s", script)
	}
		if !strings.Contains(script, `go build -trimpath -ldflags $mcpLdflags -o "$dir\emcp.exe" ./cmd/emcp`) {
			t.Fatalf("scoop installer does not build emcp.exe:\n%s", script)
		}
		if !strings.Contains(script, `building erun-app.exe requires a C compiler such as MinGW for the Wails CGO build`) {
			t.Fatalf("scoop installer does not explain the Wails CGO compiler requirement:\n%s", script)
		}
	}

func TestAptBuildScriptBuildsDebianPackageForBothExecutables(t *testing.T) {
	repoRoot := repoRootForPackagingTest(t)
	scriptPath := filepath.Join(repoRoot, "packaging", "apt", "build-deb.sh")

	scriptData, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read apt build script: %v", err)
	}
	script := string(scriptData)

	if !strings.Contains(script, `default_version_file="$repo_root/erun-devops/VERSION"`) {
		t.Fatalf("apt build script does not default to erun-devops/VERSION:\n%s", script)
	}
	if !strings.Contains(script, `go build -trimpath -ldflags "$cli_ldflags" -o "$package_root/usr/bin/erun" .`) {
		t.Fatalf("apt build script does not build erun:\n%s", script)
	}
	if !strings.Contains(script, `go build -trimpath -ldflags "$mcp_ldflags" -o "$package_root/usr/bin/emcp" ./cmd/emcp`) {
		t.Fatalf("apt build script does not build emcp:\n%s", script)
	}
	if !strings.Contains(script, `dpkg-deb --build --root-owner-group "$package_root" "$output_path"`) {
		t.Fatalf("apt build script does not emit a .deb package:\n%s", script)
	}
	if !strings.Contains(script, `dpkg-deb is required to build Debian packages; install dpkg or run this build in a Debian-compatible environment`) {
		t.Fatalf("apt build script does not explain missing dpkg-deb prerequisite:\n%s", script)
	}
	if !regexp.MustCompile(`(?m)^Package: erun$`).MatchString(script) {
		t.Fatalf("apt build script does not define erun package metadata:\n%s", script)
	}
}

func TestLinuxReleaseScriptPromptsForGitHubLogin(t *testing.T) {
	repoRoot := repoRootForPackagingTest(t)
	scriptPath := filepath.Join(repoRoot, "erun-devops", "linux", "erun-cli", "release.sh")

	scriptData, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read linux release script: %v", err)
	}
	script := string(scriptData)

	if !strings.Contains(script, `if ! gh auth status >/dev/null 2>&1; then`) {
		t.Fatalf("linux release script does not prompt for gh auth login when needed:\n%s", script)
	}
	if !strings.Contains(script, `gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key --scopes admin:public_key`) {
		t.Fatalf("linux release script does not request the admin:public_key scope during login:\n%s", script)
	}
	if !strings.Contains(script, `printf '%s-%s\n' "${ERUN_TENANT}" "${ERUN_ENVIRONMENT}"`) {
		t.Fatalf("linux release script does not derive the SSH key title from tenant and environment:\n%s", script)
	}
	if !strings.Contains(script, `gh auth refresh --hostname github.com --scopes admin:public_key`) {
		t.Fatalf("linux release script does not refresh the admin:public_key scope when needed:\n%s", script)
	}
	if !strings.Contains(script, `gh ssh-key add "$public_key_path" --title "$key_title"`) {
		t.Fatalf("linux release script does not upload the SSH key with the resolved title:\n%s", script)
	}
	if !strings.Contains(script, `grep -Fq "$public_key_data" <<<"$uploaded_keys"`) {
		t.Fatalf("linux release script does not skip SSH key upload when the key is already present:\n%s", script)
	}
	if !strings.Contains(script, `git -C "$repo_root" ls-remote --exit-code --tags origin "refs/tags/$tag"`) {
		t.Fatalf("linux release script does not check whether the release tag already exists on origin:\n%s", script)
	}
	if !strings.Contains(script, `git -C "$repo_root" push origin "$tag"`) {
		t.Fatalf("linux release script does not push a missing release tag to origin before creating the release:\n%s", script)
	}
	if !strings.Contains(script, `gh release upload "$tag" "$artifact_path" --clobber`) {
		t.Fatalf("linux release script does not upload release artifacts:\n%s", script)
	}
}

func repoRootForPackagingTest(t *testing.T) string {
	t.Helper()

	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	return filepath.Clean(filepath.Join(workDir, ".."))
}

func latestStableReleaseTag(t *testing.T, repoRoot string) string {
	t.Helper()

	cmd := exec.Command("git", "-C", repoRoot, "tag", "--list", "v*", "--sort=-version:refname")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("list git tags: %v", err)
	}

	for _, line := range strings.Split(string(output), "\n") {
		tag := strings.TrimSpace(line)
		if tag == "" || strings.Contains(tag, "-") {
			continue
		}
		return tag
	}

	t.Fatal("no stable release tag found")
	return ""
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func releaseArchiveSHA256ForTest(t *testing.T, url string) string {
	t.Helper()

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("download %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download %s: unexpected status %s", url, resp.Status)
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, resp.Body); err != nil {
		t.Fatalf("hash %s: %v", url, err)
	}
	return hex.EncodeToString(sum.Sum(nil))
}
