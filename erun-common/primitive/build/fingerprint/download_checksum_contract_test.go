package fingerprint

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Dockerfiles whose network downloads ship: each artifact is trusted only once a
// pinned SHA-256 has matched it, before anything unpacks, installs or runs it.
var checksumVerifiedDockerfiles = []string{
	filepath.Join("erun-devops", "docker", "erun-devops", "Dockerfile"),
	filepath.Join("erun-devops", "docker", "erun-backend-db", "Dockerfile"),
}

// URL prefixes of downloads still installed without a pinned checksum. An entry goes when
// its download gains one; an entry that no longer names an unverified download fails.
var downloadsPendingChecksums = []string{
	"https://download.docker.com/linux/static/stable/",
	"https://github.com/docker/buildx/releases/download/",
}

var (
	shellCommandSeparator  = regexp.MustCompile(`\s*(?:&&|\|\||;)\s*`)
	checksumArgDefault     = regexp.MustCompile(`(?m)^\s*ARG\s+(\w+)_SHA256_(AMD64|ARM64)=(\S*)`)
	sha256HexDigest        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	downloadedArtifactURLs = regexp.MustCompile(`^https?://`)
)

type dockerfileDownload struct {
	stage  string
	url    string
	output string
	rest   []string
}

// shellCommands splits one RUN into its commands, dropping the RUN keyword and its flags.
func shellCommands(run string) []string {
	fields := strings.Fields(run)
	for len(fields) > 0 && (fields[0] == "RUN" || strings.HasPrefix(fields[0], "--")) {
		fields = fields[1:]
	}
	return shellCommandSeparator.Split(strings.Join(fields, " "), -1)
}

func unquotedFields(command string) []string {
	fields := strings.Fields(command)
	for i, field := range fields {
		fields[i] = strings.Trim(field, `"'`)
	}
	return fields
}

// downloadTarget reads the URL and output file of a curl or wget command; ok is false for any other command.
func downloadTarget(command string) (url, output string, ok bool) {
	fields := unquotedFields(command)
	if len(fields) == 0 || (fields[0] != "curl" && fields[0] != "wget") {
		return "", "", false
	}
	outputFlags := map[string]bool{"-o": fields[0] == "curl", "--output": true, "-O": fields[0] == "wget", "--output-document": true}
	for i, field := range fields[1:] {
		switch {
		case outputFlags[field] && i+2 < len(fields):
			output = fields[i+2]
		case downloadedArtifactURLs.MatchString(field):
			url = field
		}
	}
	return url, output, true
}

func dockerfileDownloads(t *testing.T, path string) []dockerfileDownload {
	t.Helper()
	var downloads []dockerfileDownload
	for stage, runs := range dockerfileRunsByStage(t, path) {
		for _, run := range runs {
			commands := shellCommands(run)
			for i, command := range commands {
				if url, output, ok := downloadTarget(command); ok {
					downloads = append(downloads, dockerfileDownload{stage: stage, url: url, output: output, rest: commands[i+1:]})
				}
			}
		}
	}
	sort.Slice(downloads, func(i, j int) bool { return downloads[i].stage+downloads[i].url < downloads[j].stage+downloads[j].url })
	return downloads
}

// isChecksumVerification matches `echo|printf "<sha256>  <file>" | sha256sum -c`, naming file on the checked side.
func isChecksumVerification(command, file string) bool {
	checked, checker, piped := strings.Cut(command, "|")
	if !piped {
		return false
	}
	source := unquotedFields(checked)
	if len(source) == 0 || (source[0] != "echo" && source[0] != "printf") {
		return false
	}
	verify := strings.Fields(checker)
	if len(verify) < 2 || verify[0] != "sha256sum" {
		return false
	}
	return containsField(verify[1:], "-c", "--check") && containsField(source[1:], file)
}

func containsField(fields []string, want ...string) bool {
	for _, field := range fields {
		for _, w := range want {
			if field == w {
				return true
			}
		}
	}
	return false
}

// verifiedBeforeUse reports whether the first later command naming the download's file checks its digest.
// A download piped straight into use names no file, so nothing can check it first.
func verifiedBeforeUse(download dockerfileDownload) bool {
	if download.output == "" {
		return false
	}
	for _, command := range download.rest {
		if containsField(unquotedFields(command), download.output) {
			return isChecksumVerification(command, download.output)
		}
	}
	return false
}

func pendingChecksumPrefix(url string) (string, bool) {
	for _, prefix := range downloadsPendingChecksums {
		if strings.HasPrefix(url, prefix) {
			return prefix, true
		}
	}
	return "", false
}

func TestDockerfileDownloadsAreChecksumVerifiedBeforeUse(t *testing.T) {
	root := erunRepoRoot(t)
	pendingSeen := map[string]bool{}
	checked := 0
	for _, rel := range checksumVerifiedDockerfiles {
		for _, download := range dockerfileDownloads(t, filepath.Join(root, rel)) {
			checked++
			verified := verifiedBeforeUse(download)
			if prefix, pending := pendingChecksumPrefix(download.url); pending {
				pendingSeen[prefix] = true
				if verified {
					t.Errorf("%s (stage %s) now verifies %s: remove %q from downloadsPendingChecksums so a regression fails again", rel, download.stage, download.url, prefix)
				}
				continue
			}
			if !verified {
				t.Errorf("%s (stage %s) uses %s before checking it: download it to a file, then `echo \"${<tool>_sha256}  <file>\" | sha256sum -c -` before anything unpacks, installs or runs that file", rel, download.stage, download.url)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no curl or wget download was found; the downloads moved somewhere this test does not read")
	}
	for _, prefix := range downloadsPendingChecksums {
		if !pendingSeen[prefix] {
			t.Errorf("downloadsPendingChecksums lists %q, which no longer names a download: remove it", prefix)
		}
	}
}

// A digest pinned for one architecture only leaves the other build unverified, and a
// truncated paste would fail every build with a mismatch that looks like a bad mirror.
func TestPinnedDownloadChecksumsCoverBothArchitectures(t *testing.T) {
	root := erunRepoRoot(t)
	for _, rel := range checksumVerifiedDockerfiles {
		arches := map[string]map[string]bool{}
		for _, match := range checksumArgDefault.FindAllStringSubmatch(readDockerfileWithoutComments(t, filepath.Join(root, rel)), -1) {
			name, arch, digest := match[1], match[2], match[3]
			if !sha256HexDigest.MatchString(digest) {
				t.Errorf("%s: ARG %s_SHA256_%s=%q is not a lowercase hex SHA-256", rel, name, arch, digest)
			}
			if arches[name] == nil {
				arches[name] = map[string]bool{}
			}
			arches[name][arch] = true
		}
		if len(arches) == 0 {
			t.Errorf("%s pins no download checksum (ARG <TOOL>_SHA256_AMD64 / _ARM64)", rel)
		}
		for name, pinned := range arches {
			if !pinned["AMD64"] || !pinned["ARM64"] {
				t.Errorf("%s pins %s_SHA256 for %v only: pin both linux/amd64 and linux/arm64", rel, name, pinned)
			}
		}
	}
}
