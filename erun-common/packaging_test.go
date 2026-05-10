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
	requireNoError(t, err, "read formula")
	formula := string(formulaData)
	requireHomebrewFormulaTracksLatestTag(t, formula, latestTag)

	scoopPath := filepath.Join(repoRoot, "bucket", "erun.json")
	scoopData, err := os.ReadFile(scoopPath)
	requireNoError(t, err, "read scoop manifest")

	manifest := unmarshalScoopManifest(t, scoopData)
	requireScoopManifestTracksLatestTag(t, manifest, latestTag, latestVersion)
	requireScoopInstallerBuildsDesktopApp(t, strings.Join(manifest.Installer.Script, "\n"))
}

type scoopManifestForTest struct {
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

func requireHomebrewFormulaTracksLatestTag(t *testing.T, formula, latestTag string) {
	t.Helper()
	requireStringContains(t, formula, `url "https://github.com/sophium/erun/archive/refs/tags/`+latestTag+`.tar.gz"`, "formula does not target latest stable tag "+latestTag)
	requireStringContains(t, formula, `bin/"erun"`, "formula does not build erun")
	requireStringContains(t, formula, `bin/"emcp"`, "formula does not build emcp")
	requireStringContains(t, formula, `bin/"eapi"`, "formula does not build eapi")
	requireStringContains(t, formula, `depends_on "node" => :build`, "formula missing node dependency")
	requireStringContains(t, formula, `depends_on "yarn" => :build`, "formula missing yarn dependency")
	requireStringContains(t, formula, `system "yarn", "install", "--frozen-lockfile"`, "formula does not install frontend dependencies")
	requireStringContains(t, formula, `go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2`, "formula does not resolve Wails version")
	requireStringContains(t, formula, `system "go", "install", "github.com/wailsapp/wails/v2/cmd/wails@#{wails_version}"`, "formula does not install Wails")
	requireStringContains(t, formula, `system wails_bin, "generate", "module"`, "formula does not generate frontend bindings")
	requireStringContains(t, formula, `system "yarn", "build"`, "formula does not build frontend assets")
	requireFormulaChecksum(t, formula, latestTag)
}

func requireFormulaChecksum(t *testing.T, formula, latestTag string) {
	t.Helper()
	formulaSHA := regexp.MustCompile(`(?m)^  sha256 "([0-9a-f]+)"$`).FindStringSubmatch(formula)
	requireEqual(t, len(formulaSHA), 2, "formula sha256 match count")
	gotFormulaSHA := releaseArchiveSHA256ForTest(t, "https://github.com/sophium/erun/archive/refs/tags/"+latestTag+".tar.gz")
	requireEqual(t, formulaSHA[1], gotFormulaSHA, "formula sha256")
}

func unmarshalScoopManifest(t *testing.T, data []byte) scoopManifestForTest {
	t.Helper()
	var manifest scoopManifestForTest
	requireNoError(t, json.Unmarshal(data, &manifest), "unmarshal scoop manifest")
	return manifest
}

func requireScoopManifestTracksLatestTag(t *testing.T, manifest scoopManifestForTest, latestTag, latestVersion string) {
	t.Helper()
	requireEqual(t, manifest.Version, latestVersion, "scoop version")
	wantZipURL := "https://github.com/sophium/erun/archive/refs/tags/" + latestTag + ".zip"
	requireEqual(t, manifest.URL, wantZipURL, "scoop url")
	requireEqual(t, manifest.Hash, releaseArchiveSHA256ForTest(t, manifest.URL), "scoop hash")
	requireEqual(t, manifest.ExtractDir, "erun-"+latestVersion, "scoop extract_dir")
	requireScoopManifestCommands(t, manifest)
}

func requireScoopManifestCommands(t *testing.T, manifest scoopManifestForTest) {
	t.Helper()
	requireCondition(t, containsString(manifest.Depends, "go"), "scoop manifest missing go dependency: %+v", manifest.Depends)
	requireCondition(t, containsString(manifest.Depends, "mingw"), "scoop manifest missing mingw dependency: %+v", manifest.Depends)
	requireCondition(t, containsString(manifest.Depends, "nodejs"), "scoop manifest missing nodejs dependency: %+v", manifest.Depends)
	requireCondition(t, containsString(manifest.Depends, "yarn"), "scoop manifest missing yarn dependency: %+v", manifest.Depends)
	requireCondition(t, containsString(manifest.Bin, "erun.exe") && containsString(manifest.Bin, "emcp.exe") && containsString(manifest.Bin, "eapi.exe"), "scoop manifest does not shim expected executables: %+v", manifest.Bin)
}

func requireScoopInstallerBuildsDesktopApp(t *testing.T, script string) {
	t.Helper()
	requireStringContains(t, script, `go build -trimpath -ldflags $cliLdflags -o "$dir\erun.exe" .`, "scoop installer does not build erun.exe")
	requireStringContains(t, script, `go build -trimpath -ldflags $mcpLdflags -o "$dir\emcp.exe" ./cmd/emcp`, "scoop installer does not build emcp.exe")
	requireStringContains(t, script, `go build -trimpath -ldflags $apiLdflags -o "$dir\eapi.exe" ./cmd/eapi`, "scoop installer does not build eapi.exe")
	requireStringContains(t, script, `building erun-app.exe requires a C compiler such as MinGW for the Wails CGO build`, "scoop installer does not explain Wails CGO compiler requirement")
	requireStringContains(t, script, `yarn install --frozen-lockfile`, "scoop installer does not install frontend dependencies")
	requireStringContains(t, script, `go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2`, "scoop installer does not resolve Wails version")
	requireStringContains(t, script, `go install "github.com/wailsapp/wails/v2/cmd/wails@$wailsVersion"`, "scoop installer does not install Wails")
	requireStringContains(t, script, `generate module`, "scoop installer does not generate frontend bindings")
	requireStringContains(t, script, `yarn build`, "scoop installer does not build frontend assets")
}

func TestAptBuildScriptBuildsDebianPackageForExecutables(t *testing.T) {
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
	if !strings.Contains(script, `go build -trimpath -ldflags "$api_ldflags" -o "$package_root/usr/bin/eapi" ./cmd/eapi`) {
		t.Fatalf("apt build script does not build eapi:\n%s", script)
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
	requireNoError(t, err, "read linux release script")
	script := string(scriptData)

	requireLinuxReleaseScriptGitHubFlow(t, script)
}

func requireLinuxReleaseScriptGitHubFlow(t *testing.T, script string) {
	t.Helper()
	requireStringContains(t, script, `if ! gh auth status >/dev/null 2>&1; then`, "linux release script does not prompt for gh auth login")
	requireStringContains(t, script, `gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key --scopes admin:public_key`, "linux release script does not request admin:public_key scope")
	requireStringContains(t, script, `printf '%s-%s\n' "${ERUN_TENANT}" "${ERUN_ENVIRONMENT}"`, "linux release script does not derive SSH key title")
	requireStringContains(t, script, `gh auth refresh --hostname github.com --scopes admin:public_key`, "linux release script does not refresh admin:public_key scope")
	requireStringContains(t, script, `gh ssh-key add "$public_key_path" --title "$key_title"`, "linux release script does not upload SSH key")
	requireStringContains(t, script, `grep -Fq "$public_key_data" <<<"$uploaded_keys"`, "linux release script does not skip existing SSH key")
	requireStringContains(t, script, `git -C "$repo_root" ls-remote --exit-code --tags origin "refs/tags/$tag"`, "linux release script does not check origin tag")
	requireStringContains(t, script, `git -C "$repo_root" push origin "$tag"`, "linux release script does not push missing tag")
	requireStringContains(t, script, `gh release upload "$tag" "$artifact_path" --clobber`, "linux release script does not upload artifacts")
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
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download %s: unexpected status %s", url, resp.Status)
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, resp.Body); err != nil {
		t.Fatalf("hash %s: %v", url, err)
	}
	return hex.EncodeToString(sum.Sum(nil))
}
