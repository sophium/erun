package shared

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

// The local version lease is per environment, so releases from different environments
// cannot see each other. A remote claim ref is repository-global: creating a missing ref
// and --force-with-lease are git's compare-and-swap.
const (
	// ReleaseRepoClaimRemote is the remote every release publishes through, so all racers see it.
	ReleaseRepoClaimRemote    = "origin"
	releaseRepoClaimRefPrefix = "refs/erun/release-claim/"
	// releaseRepoClaimProbeRefPrefix avoids writing FETCH_HEAD, which concurrent release git commands read.
	releaseRepoClaimProbeRefPrefix = "refs/erun/_release-claim-probe/"
	releaseRepoClaimMaxAttempts    = 3
)

// ReleaseRepoClaimRecord carries Environment, not a pid: the holder may be in any environment.
type ReleaseRepoClaimRecord struct {
	Holder      model.EnvironmentActivityLeaseHolder `json:"holder"`
	Environment string                               `json:"environment,omitempty"`
	StartedAt   time.Time                            `json:"startedAt"`
	ExpiresAt   time.Time                            `json:"expiresAt"`
}

// ReleaseRepoClaimConflictError carries Now so Error is a pure function of its fields.
type ReleaseRepoClaimConflictError struct {
	Version     string
	Holder      model.EnvironmentActivityLeaseHolder
	Environment string
	StartedAt   time.Time
	ExpiresAt   time.Time
	Now         time.Time
}

// Error names both remedies: a same-environment holder is reclaimed once gone, another
// environment's must lapse; the ref is the manual escape hatch.
func (e *ReleaseRepoClaimConflictError) Error() string {
	return fmt.Sprintf("%s is already being released by %s (running for %s) — a holder in this environment is reclaimed automatically on the next attempt once its process is gone, and a holder in another environment once its claim lapses. If you have established that holder is gone, clear it with: git push %s --delete %s",
		strings.TrimSpace(e.Version),
		ReleaseClaimHolderDescription(e.Holder, e.Environment),
		releaseClaimRunningFor(e.StartedAt, e.Now),
		ReleaseRepoClaimRemote,
		ReleaseRepoClaimRef(e.Version))
}

// releaseClaimRunningFor reports a zero StartedAt (older records) as an unknown duration.
func releaseClaimRunningFor(startedAt, now time.Time) string {
	if startedAt.IsZero() {
		return "an unknown duration"
	}
	return now.Sub(startedAt).Round(time.Second).String()
}

// ReleaseClaimHolderDescription appends each half only when present.
func ReleaseClaimHolderDescription(holder model.EnvironmentActivityLeaseHolder, environment string) string {
	desc := holder.String()
	environment = strings.TrimSpace(environment)
	if environment == "" {
		return desc
	}
	if desc == "an unnamed holder" {
		return "environment " + environment
	}
	return desc + ", environment " + environment
}

func ReleaseRepoClaimRef(version string) string {
	return releaseRepoClaimRefPrefix + core.SanitizeForFilename(strings.TrimSpace(version))
}

func releaseRepoClaimProbeRef(version string) string {
	return releaseRepoClaimProbeRefPrefix + core.SanitizeForFilename(strings.TrimSpace(version))
}

// releaseRepoClaimLocalHolder reports whether the caller's environment still has a live holder;
// nil means unknown, leaving expiry as the only rule.
type releaseRepoClaimLocalHolder func() bool

// TakeReleaseRepoClaim creates, reclaims, or refuses the remote claim. An empty sha with nil
// error means the remote was unreadable and the release proceeds without it.
// localHolderAlive must be read before the caller takes its own local claim.
func TakeReleaseRepoClaim(ctx core.Context, projectRoot, environment, version string, holder model.EnvironmentActivityLeaseHolder, now time.Time, localHolderAlive releaseRepoClaimLocalHolder) (string, error) {
	ref := ReleaseRepoClaimRef(version)

	for attempt := 0; attempt < releaseRepoClaimMaxAttempts; attempt++ {
		existingSHA, exists, readable := readReleaseRepoClaimRef(ctx, projectRoot, ref)
		if !readable {
			ctx.Trace("release: could not read " + ref + " on " + ReleaseRepoClaimRemote + "; proceeding without the repository-global release claim")
			return "", nil
		}

		if !exists {
			newSHA, err := WriteReleaseRepoClaimBlob(ctx, projectRoot, environment, holder, now, now)
			if err != nil {
				return "", err
			}
			if err := GitPushCreateRef(ctx, projectRoot, ReleaseRepoClaimRemote, newSHA, ref); err == nil {
				return newSHA, nil
			}
			continue // somebody else's create landed first; loop and re-read who holds it.
		}

		record, err := LoadReleaseRepoClaimRecord(ctx, projectRoot, ReleaseRepoClaimRemote, ref, version)
		if err != nil {
			return "", fmt.Errorf("release: %s exists on %s but its content could not be read: %w", ref, ReleaseRepoClaimRemote, err)
		}
		if releaseRepoClaimHeld(record, now, environment, localHolderAlive) {
			return "", &ReleaseRepoClaimConflictError{Version: version, Holder: record.Holder, Environment: record.Environment, StartedAt: record.StartedAt, ExpiresAt: record.ExpiresAt, Now: now}
		}

		// A reclaim is a new holder with its own StartedAt.
		newSHA, err := WriteReleaseRepoClaimBlob(ctx, projectRoot, environment, holder, now, now)
		if err != nil {
			return "", err
		}
		if err := gitPushUpdateRefWithLease(ctx, projectRoot, ReleaseRepoClaimRemote, newSHA, ref, existingSHA); err == nil {
			return newSHA, nil
		}
		// Somebody else's push landed first; re-read.
	}
	return "", fmt.Errorf("release: could not take the repository-global claim for %s: contended", version)
}

// RenewReleaseRepoClaim CAS-extends the caller's own claim, best-effort, carrying the
// original StartedAt forward from its own locally-present blob.
func RenewReleaseRepoClaim(ctx core.Context, projectRoot, environment, version string, holder model.EnvironmentActivityLeaseHolder, now time.Time, currentSHA string) (string, error) {
	existing, err := ReadReleaseRepoClaimBlob(ctx, projectRoot, currentSHA)
	if err != nil {
		return "", err
	}
	newSHA, err := WriteReleaseRepoClaimBlob(ctx, projectRoot, environment, holder, existing.StartedAt, now)
	if err != nil {
		return "", err
	}
	ref := ReleaseRepoClaimRef(version)
	if err := gitPushUpdateRefWithLease(ctx, projectRoot, ReleaseRepoClaimRemote, newSHA, ref, currentSHA); err != nil {
		return "", err
	}
	return newSHA, nil
}

// deleteReleaseRepoClaim only removes the ref if it still holds the caller's sha.
func deleteReleaseRepoClaim(ctx core.Context, projectRoot, version, currentSHA string) error {
	ref := ReleaseRepoClaimRef(version)
	lease := fmt.Sprintf("--force-with-lease=%s:%s", ref, currentSHA)
	ctx.TraceCommand(projectRoot, "git", "push", lease, ReleaseRepoClaimRemote, "--delete", ref)
	return runReleaseClaimPush(ctx, projectRoot, "push", lease, ReleaseRepoClaimRemote, "--delete", ref)
}

// releaseRepoClaimHeld: a claim from the caller's own environment is reclaimable as soon as
// the local lease shows no live holder (crashed holders never delete it). Other or
// unrecorded environments fall back to expiry only.
func releaseRepoClaimHeld(record ReleaseRepoClaimRecord, now time.Time, environment string, localHolderAlive releaseRepoClaimLocalHolder) bool {
	if record.ExpiresAt.IsZero() || !now.Before(record.ExpiresAt) {
		return false
	}
	if environment == "" || record.Environment != environment || localHolderAlive == nil {
		return true
	}
	return localHolderAlive()
}

func WriteReleaseRepoClaimBlob(ctx core.Context, projectRoot, environment string, holder model.EnvironmentActivityLeaseHolder, startedAt, now time.Time) (string, error) {
	record := ReleaseRepoClaimRecord{
		Holder:      holder,
		Environment: environment,
		StartedAt:   startedAt,
		ExpiresAt:   now.Add(ReleaseVersionClaimTTL),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	ctx.TraceCommand(projectRoot, "git", "hash-object", "-w", "--stdin")
	cmd := core.Command("git", "-C", projectRoot, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(string(data))
	output, err := cmd.Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// ReadReleaseRepoClaimBlob reads a blob this process wrote, from the local object database.
func ReadReleaseRepoClaimBlob(ctx core.Context, projectRoot, sha string) (ReleaseRepoClaimRecord, error) {
	ctx.TraceCommand(projectRoot, "git", "cat-file", "-p", sha)
	output, err := core.Command("git", "-C", projectRoot, "cat-file", "-p", sha).Output()
	if err != nil {
		return ReleaseRepoClaimRecord{}, err
	}
	var record ReleaseRepoClaimRecord
	if err := json.Unmarshal(output, &record); err != nil {
		return ReleaseRepoClaimRecord{}, err
	}
	return record, nil
}

// GitLsRemoteRef reads one remote ref without writing local state. ok is false when missing
// or unreadable; err tells them apart.
func GitLsRemoteRef(ctx core.Context, projectRoot, remote, ref string) (sha string, ok bool, err error) {
	ctx.TraceCommand(projectRoot, "git", "ls-remote", remote, ref)
	output, err := core.Command("git", "-C", projectRoot, "ls-remote", remote, ref).Output()
	if err != nil {
		return "", false, err
	}
	line := strings.TrimSpace(string(output))
	if line == "" {
		return "", false, nil
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false, nil
	}
	return fields[0], true, nil
}

// LoadReleaseRepoClaimRecord fetches ref into a throwaway probe ref and always removes it.
func LoadReleaseRepoClaimRecord(ctx core.Context, projectRoot, remote, ref, version string) (ReleaseRepoClaimRecord, error) {
	probe := releaseRepoClaimProbeRef(version)
	defer func() {
		_ = core.Command("git", "-C", projectRoot, "update-ref", "-d", probe).Run()
	}()

	fetchSpec := "+" + ref + ":" + probe
	ctx.TraceCommand(projectRoot, "git", "fetch", "--no-write-fetch-head", remote, fetchSpec)
	if err := core.Command("git", "-C", projectRoot, "fetch", "--no-write-fetch-head", remote, fetchSpec).Run(); err != nil {
		return ReleaseRepoClaimRecord{}, err
	}
	output, err := core.Command("git", "-C", projectRoot, "cat-file", "-p", probe).Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return ReleaseRepoClaimRecord{}, fmt.Errorf("%w: %s", err, stderr)
		}
		return ReleaseRepoClaimRecord{}, err
	}
	var record ReleaseRepoClaimRecord
	if err := json.Unmarshal(output, &record); err != nil {
		return ReleaseRepoClaimRecord{}, err
	}
	return record, nil
}

func GitPushCreateRef(ctx core.Context, projectRoot, remote, sha, ref string) error {
	ctx.TraceCommand(projectRoot, "git", "push", remote, sha+":"+ref)
	return runReleaseClaimPush(ctx, projectRoot, "push", remote, sha+":"+ref)
}

func gitPushUpdateRefWithLease(ctx core.Context, projectRoot, remote, sha, ref, expectedSHA string) error {
	lease := fmt.Sprintf("--force-with-lease=%s:%s", ref, expectedSHA)
	ctx.TraceCommand(projectRoot, "git", "push", lease, remote, sha+":"+ref)
	return runReleaseClaimPush(ctx, projectRoot, "push", lease, remote, sha+":"+ref)
}

// runReleaseClaimPush reports a ruleset bypass on the claim push too; it bypasses the git seam,
// so the capture is wired here.
func runReleaseClaimPush(ctx core.Context, projectRoot string, args ...string) error {
	var pushOutput strings.Builder
	cmd := core.Command("git", append([]string{"-C", projectRoot}, args...)...)
	cmd.Stderr = releasePushStderrWriter(ctx, &pushOutput)
	err := cmd.Run()
	gitx.ReportRulesetBypassFromPushArgs(ctx, args, pushOutput.String())
	return err
}

func readReleaseRepoClaimRef(ctx core.Context, projectRoot, ref string) (sha string, exists, readable bool) {
	sha, exists, err := GitLsRemoteRef(ctx, projectRoot, ReleaseRepoClaimRemote, ref)
	return sha, exists, err == nil
}
