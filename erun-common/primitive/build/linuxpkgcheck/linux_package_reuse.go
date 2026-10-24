package linuxpkgcheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	fingerprintpkg "github.com/sophium/erun/erun-common/primitive/build/fingerprint"
)

// A package has no image to promote, so a plain build's passing check stage is recorded locally
// per fingerprint and daemon platform. Releases never consult the record.

const (
	linuxPackageTestRecordsKept = 8
	// linuxPackageTestRecordMaxAge bounds drift a moved base tag can hide, since the fingerprint hashes only the tag.
	linuxPackageTestRecordMaxAge = 30 * 24 * time.Hour
)

type linuxPackageTestPass struct {
	Fingerprint string    `json:"fingerprint"`
	Platform    string    `json:"platform"`
	PassedAt    time.Time `json:"passedAt"`
}

type linuxPackageTestRecord struct {
	Passed []linuxPackageTestPass `json:"passed"`
}

var (
	LinuxPackageTestRecordDir = defaultLinuxPackageTestRecordDir
	linuxPackageTestRecordNow = time.Now
)

func defaultLinuxPackageTestRecordDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".erun", "package-tests"), nil
}

func linuxPackageTestRecordPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", errors.New("linux package name cannot name a record file: " + name)
	}
	dir, err := LinuxPackageTestRecordDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

// LinuxPackageDaemonPlatform is the daemon's platform, which differs from the host's when
// DOCKER_HOST names a remote daemon of another architecture.
var LinuxPackageDaemonPlatform = ReadDockerDaemonPlatform

const linuxPackageDaemonPlatformTimeout = 30 * time.Second

var dockerDaemonPlatformPattern = regexp.MustCompile(`^linux/[a-z0-9_]+$`)

// ReadDockerDaemonPlatform asks the docker daemon for its platform, under a deadline.
func ReadDockerDaemonPlatform() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), linuxPackageDaemonPlatformTimeout)
	defer cancel()
	out, err := core.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Os}}/{{.Server.Arch}}").Output()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("docker version: %w", ctxErr)
	}
	if err != nil {
		return "", fmt.Errorf("docker version: %w", err)
	}
	platform := strings.TrimSpace(string(out))
	if !dockerDaemonPlatformPattern.MatchString(platform) {
		return "", fmt.Errorf("docker version reported %q, not a linux platform", platform)
	}
	return platform, nil
}

// ApplyLinuxPackageReuse marks packages with a recorded pass on the daemon's platform for reuse.
// A package that cannot be fingerprinted runs, records nothing, and says why.
func ApplyLinuxPackageReuse(packages []model.LinuxPackageSpec) []model.LinuxPackageSpec {
	out := slices.Clone(packages)
	daemonPlatform := sync.OnceValues(LinuxPackageDaemonPlatform)
	for i := range out {
		target, err := linuxPackageCheckTarget(out[i])
		if err != nil {
			continue
		}
		fingerprint, err := linuxPackageCheckFingerprint(out[i], target)
		if err != nil {
			out[i].NoReuseReason = "it could not be fingerprinted: " + err.Error()
			continue
		}
		out[i].Fingerprint = fingerprint
		platform, err := daemonPlatform()
		if err != nil {
			out[i].NoReuseReason = "the docker daemon's platform could not be read, so no pass for fingerprint fp-" + fingerprint + " is reused or recorded: " + err.Error()
			continue
		}
		out[i].Platform = platform
		out[i].Reuse = linuxPackageTestPassed(out[i].Name, fingerprint, platform)
	}
	return out
}

// linuxPackageCheckFingerprint hashes the Dockerfile and context COPY sources of the check stage
// and its ancestors. No build args are hashed; the version reaches only later stages.
func linuxPackageCheckFingerprint(pkg model.LinuxPackageSpec, target string) (string, error) {
	data, err := os.ReadFile(pkg.DockerfilePath)
	if err != nil {
		return "", err
	}
	rel, err := fingerprintpkg.DockerfileRelativePath(pkg.DockerfilePath, pkg.ContextDir)
	if err != nil {
		return "", err
	}
	sources := []string{rel}
	for _, copySources := range dockerfileStageCopySources(string(data), target) {
		sources = append(sources, copySources...)
	}
	hasher := sha256.New()
	if _, err := io.WriteString(hasher, "linux-package-target="+target+"\n\x00"); err != nil {
		return "", err
	}
	if err := fingerprintpkg.HashContextSourcesInto(hasher, pkg.ContextDir, sources); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil))[:16], nil
}

type copyStage struct {
	name string
	deps []string
	body strings.Builder
}

var (
	copyStageFromPattern      = regexp.MustCompile(`(?i)^\s*FROM\s+(.+?)\s*$`)
	copyStageCopyPattern      = regexp.MustCompile(`(?i)^\s*COPY\s`)
	copyStageFromFlagPattern  = regexp.MustCompile(`(?i)(?:^|\s)--from=(\S+)`)
	copyStageMountFromPattern = regexp.MustCompile(`(?i)--mount=\S*\bfrom=([^,\s]+)`)
)

// dockerfileStageCopySources returns the context COPY sources of target and the stages it builds on.
// An unknown target falls back to every stage.
func dockerfileStageCopySources(dockerfile, target string) [][]string {
	stages := parseCopyStages(dockerfile)
	reachable := reachableCopyStages(stages, strings.ToLower(target))
	if reachable == nil {
		return fingerprintpkg.ParseDockerfileCopyInstructions(dockerfile)
	}
	var sources [][]string
	for i := range stages {
		if reachable[i] {
			sources = append(sources, fingerprintpkg.ParseDockerfileCopyInstructions(stages[i].body.String())...)
		}
	}
	return sources
}

func parseCopyStages(dockerfile string) []*copyStage {
	var stages []*copyStage
	for _, line := range strings.Split(dockerfile, "\n") {
		if match := copyStageFromPattern.FindStringSubmatch(line); match != nil {
			base, name := parseDockerfileFromArgs(match[1])
			stages = append(stages, &copyStage{name: name, deps: []string{base}})
		}
		if len(stages) == 0 {
			continue
		}
		stage := stages[len(stages)-1]
		stage.body.WriteString(line + "\n")
		if match := copyStageMountFromPattern.FindStringSubmatch(line); match != nil {
			stage.deps = append(stage.deps, strings.ToLower(match[1]))
		}
		if !copyStageCopyPattern.MatchString(line) {
			continue
		}
		if match := copyStageFromFlagPattern.FindStringSubmatch(line); match != nil {
			stage.deps = append(stage.deps, strings.ToLower(match[1]))
		}
	}
	return stages
}

func parseDockerfileFromArgs(args string) (base, name string) {
	fields := make([]string, 0, 3)
	for _, field := range strings.Fields(args) {
		if !strings.HasPrefix(field, "--") {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return "", ""
	}
	base = strings.ToLower(fields[0])
	if len(fields) >= 3 && strings.EqualFold(fields[1], "AS") {
		name = strings.ToLower(fields[2])
	}
	return base, name
}

func reachableCopyStages(stages []*copyStage, target string) map[int]bool {
	start, ok := copyStageIndex(stages, target)
	if !ok || stages[start].name == "" {
		return nil
	}
	reachable := map[int]bool{start: true}
	queue := []int{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dep := range stages[current].deps {
			// A stage can only build on one declared before it.
			i, ok := copyStageIndex(stages, dep)
			if ok && i < current && !reachable[i] {
				reachable[i] = true
				queue = append(queue, i)
			}
		}
	}
	return reachable
}

// copyStageIndex resolves an index or stage name; anything else is an external image.
func copyStageIndex(stages []*copyStage, ref string) (int, bool) {
	if n, err := strconv.Atoi(ref); err == nil && n >= 0 && n < len(stages) {
		return n, true
	}
	for i, stage := range stages {
		if stage.name != "" && stage.name == ref {
			return i, true
		}
	}
	return 0, false
}

func readLinuxPackageTestRecord(name string) (linuxPackageTestRecord, string, error) {
	path, err := linuxPackageTestRecordPath(name)
	if err != nil {
		return linuxPackageTestRecord{}, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return linuxPackageTestRecord{}, path, err
	}
	var record linuxPackageTestRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return linuxPackageTestRecord{}, path, err
	}
	return record, path, nil
}

// linuxPackageTestPassed reports a recorded, unexpired pass; an unreadable record is no pass.
func linuxPackageTestPassed(name, fingerprint, platform string) bool {
	record, _, err := readLinuxPackageTestRecord(name)
	if err != nil {
		return false
	}
	cutoff := linuxPackageTestRecordNow().Add(-linuxPackageTestRecordMaxAge)
	return slices.ContainsFunc(record.Passed, func(pass linuxPackageTestPass) bool {
		return pass.Fingerprint == fingerprint && pass.Platform == platform && pass.PassedAt.After(cutoff)
	})
}

func RecordLinuxPackageTestPass(name, fingerprint, platform string) error {
	record, path, err := readLinuxPackageTestRecord(name)
	if path == "" {
		return err
	}
	now := linuxPackageTestRecordNow()
	cutoff := now.Add(-linuxPackageTestRecordMaxAge)
	kept := make([]linuxPackageTestPass, 0, len(record.Passed)+1)
	for _, pass := range record.Passed {
		if pass.PassedAt.After(cutoff) && (pass.Fingerprint != fingerprint || pass.Platform != platform) {
			kept = append(kept, pass)
		}
	}
	kept = append(kept, linuxPackageTestPass{Fingerprint: fingerprint, Platform: platform, PassedAt: now.UTC()})
	if len(kept) > linuxPackageTestRecordsKept {
		kept = kept[len(kept)-linuxPackageTestRecordsKept:]
	}
	data, err := json.MarshalIndent(linuxPackageTestRecord{Passed: kept}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return config.WriteFileAtomic(path, append(data, '\n'), 0o600)
}
