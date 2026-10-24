package eruncommon

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
)

// A linux package component never produces an image, so it has no fp tag to
// promote from. Its reuse record is a local one instead: after a plain build's
// check stage passes, the package's fingerprint is recorded under the user's
// erun state directory, and the next plain build with the same fingerprint on
// the same docker daemon platform skips the stage: `test: reused` for a `test`
// stage, `test: none declared` for a package that only has a `build` stage.
// The fingerprint hashes the Dockerfile's text, so a base image tag that moves
// to new content does not change it; only the record's expiry bounds that.
// Only a plain build reads or writes the record: a release always builds its
// `dist` stage (exportReleaseLinuxPackages never consults it).

const (
	// linuxPackageTestRecordsKept bounds each package's record to its most
	// recent passes, so a long-lived checkout does not grow it without limit.
	linuxPackageTestRecordsKept = 8
	// linuxPackageTestRecordMaxAge expires a pass, so a record is never older
	// than the toolchain drift a pinned base tag can still hide.
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

// linuxPackageTestRecordDir and linuxPackageTestRecordNow are package-level
// seams, like timingRecordDir, so a test points them at its own tree and clock.
var (
	linuxPackageTestRecordDir = defaultLinuxPackageTestRecordDir
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
	dir, err := linuxPackageTestRecordDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

// linuxPackageDaemonPlatform is the platform a plain check build runs on. The
// build passes no --platform, so BuildKit builds for the docker daemon's own
// platform, which is not the host's when DOCKER_HOST names a remote daemon of
// another architecture. It is a package-level seam so a test can name one.
var linuxPackageDaemonPlatform = readDockerDaemonPlatform

const linuxPackageDaemonPlatformTimeout = 30 * time.Second

var dockerDaemonPlatformPattern = regexp.MustCompile(`^linux/[a-z0-9_]+$`)

// readDockerDaemonPlatform asks the daemon the builds run on for its platform,
// under a deadline so a daemon that never answers cannot hold the build.
func readDockerDaemonPlatform() (string, error) {
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

// applyLinuxPackageReuse fingerprints each package's check stage and marks the
// ones with a recorded pass on the daemon's platform for reuse. The daemon is
// asked once per build. A package that cannot be fingerprinted, or whose
// daemon platform is unknown, runs and records nothing, and says why.
func applyLinuxPackageReuse(packages []LinuxPackageSpec) []LinuxPackageSpec {
	out := slices.Clone(packages)
	daemonPlatform := sync.OnceValues(linuxPackageDaemonPlatform)
	for i := range out {
		target, err := linuxPackageCheckTarget(out[i])
		if err != nil {
			continue
		}
		fingerprint, err := linuxPackageCheckFingerprint(out[i], target)
		if err != nil {
			out[i].noReuseReason = "it could not be fingerprinted: " + err.Error()
			continue
		}
		out[i].Fingerprint = fingerprint
		platform, err := daemonPlatform()
		if err != nil {
			out[i].noReuseReason = "the docker daemon's platform could not be read, so no pass for fingerprint fp-" + fingerprint + " is reused or recorded: " + err.Error()
			continue
		}
		out[i].Platform = platform
		out[i].Reuse = linuxPackageTestPassed(out[i].Name, fingerprint, platform)
	}
	return out
}

// linuxPackageCheckFingerprint is computeBuildFingerprint's input set for a
// package's check stage: the Dockerfile, every file the stages that stage is
// built from COPY out of the context, less what the context's ignore files
// exclude. A plain check build passes no build arg, so none is hashed; the
// version reaches only the stages after it, so neither ERUN_VERSION nor the
// VERSION file a `package` stage copies moves it.
func linuxPackageCheckFingerprint(pkg LinuxPackageSpec, target string) (string, error) {
	data, err := os.ReadFile(pkg.DockerfilePath)
	if err != nil {
		return "", err
	}
	rel, err := dockerfileRelativePath(pkg.DockerfilePath, pkg.ContextDir)
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
	if err := hashContextSourcesInto(hasher, pkg.ContextDir, sources); err != nil {
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

// dockerfileStageCopySources returns the context COPY sources of target and of
// every stage it is built from, through FROM <stage>, COPY --from=<stage> and
// RUN --mount=...,from=<stage>: the stages BuildKit runs for that target. A
// target the Dockerfile does not name falls back to every stage's sources.
func dockerfileStageCopySources(dockerfile, target string) [][]string {
	stages := parseCopyStages(dockerfile)
	reachable := reachableCopyStages(stages, strings.ToLower(target))
	if reachable == nil {
		return parseDockerfileCopyInstructions(dockerfile)
	}
	var sources [][]string
	for i := range stages {
		if reachable[i] {
			sources = append(sources, parseDockerfileCopyInstructions(stages[i].body.String())...)
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

// parseDockerfileFromArgs reads `[--platform=...] <base> [AS <name>]`.
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

// copyStageIndex resolves a stage reference: an index, or a stage name. Any
// other reference is an external image, which the context does not feed.
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

// linuxPackageTestPassed reports a recorded, unexpired pass. A record that
// cannot be read is no pass: the package runs, which is never wrong.
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

// recordLinuxPackageTestPass records a pass, dropping expired entries and all
// but the most recent linuxPackageTestRecordsKept.
func recordLinuxPackageTestPass(name, fingerprint, platform string) error {
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
