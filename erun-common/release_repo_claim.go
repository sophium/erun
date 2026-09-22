package eruncommon

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The exclusive activity lease release_claim.go added scopes its claim to one
// environment's own on-disk store, so two orchestrators driving different
// environments take their claims in different scopes and are structurally
// invisible to each other — the collision that actually happens in
// production, since a release can run from any environment. The resource
// genuinely being contended is the repository itself: one VERSION, one tag
// namespace, one main. This file adds a second, repository-global claim on a
// remote git ref, which every orchestrator that can push to the release's
// origin can see and contend on, with no shared store beyond the repository
// they are already racing to change.
//
// A plain `git push <object>:<ref>` for a ref that does not yet exist is
// git's own compare-and-swap: the client claims the ref currently has no
// value, and the server rejects the update the instant somebody else's push
// already gave it one — the same atomicity O_CREATE|O_EXCL gives the local
// exclusive lease's file create. `--force-with-lease=<ref>:<expected>` gives
// the identical compare-and-swap for a renewal or a reclaim, so a stale
// holder can never be overwritten by a caller racing another live one.
const (
	// releaseRepoClaimRemote is the remote every release publishes through,
	// so it is the one namespace every orchestrator racing this repository
	// can actually reach.
	releaseRepoClaimRemote = "origin"
	// releaseRepoClaimRefPrefix namespaces the claim refs away from tags and
	// branches a human would create.
	releaseRepoClaimRefPrefix = "refs/erun/release-claim/"
	// releaseRepoClaimProbeRefPrefix is a throwaway local ref used only to
	// read a remote claim's content without writing FETCH_HEAD, which the
	// release's own git commands (running concurrently with this claim's
	// renewal ticker, in the same checkout) read and rely on.
	releaseRepoClaimProbeRefPrefix = "refs/erun/_release-claim-probe/"
	// releaseRepoClaimMaxAttempts bounds the retry when a create or a reclaim
	// loses a race: one retry re-reads and reacts to whoever just won.
	releaseRepoClaimMaxAttempts = 3
)

// releaseRepoClaimRecord is the JSON stored in the claim ref's blob. It
// carries Environment rather than PID: every environment that can push to
// the release's origin can race this claim, so the environment is what
// actually identifies a losing caller's counterpart, while a pid only ever
// named a process in a pod that may already be gone by the time anyone reads
// it back.
type releaseRepoClaimRecord struct {
	Holder      EnvironmentActivityLeaseHolder `json:"holder"`
	Environment string                         `json:"environment,omitempty"`
	StartedAt   time.Time                      `json:"startedAt"`
	ExpiresAt   time.Time                      `json:"expiresAt"`
}

// ReleaseRepoClaimConflictError names the still-live holder of the
// repository-global claim, in the same wording the local lease's refusal
// uses, so a second orchestrator sees one consistent message regardless of
// which of the two claims refused it. Now is carried alongside StartedAt
// (rather than computing a duration with time.Now() inside Error) so the
// message is a pure function of the refusal's own fields and stays testable
// against a fixed clock.
type ReleaseRepoClaimConflictError struct {
	Version     string
	Holder      EnvironmentActivityLeaseHolder
	Environment string
	StartedAt   time.Time
	ExpiresAt   time.Time
	Now         time.Time
}

// Error names both remedies, and neither promises more than the reclaim rule
// delivers. A holder in the refusing environment is reclaimed by that
// environment's own liveness probe as soon as its process is gone (see
// releaseRepoClaimHeld); a holder in another environment can only be waited
// out to the claim's lapse, since nothing here can see whether its process is
// still there. The ref itself is named as the deliberate escape hatch: it is
// the only handle a claim has, and an operator who has established that the
// holder is gone needs it to clear a version rather than abandon it.
func (e *ReleaseRepoClaimConflictError) Error() string {
	return fmt.Sprintf("%s is already being released by %s (running for %s) — a holder in this environment is reclaimed automatically on the next attempt once its process is gone, and a holder in another environment once its claim lapses. If you have established that holder is gone, clear it with: git push %s --delete %s",
		strings.TrimSpace(e.Version),
		releaseClaimHolderDescription(e.Holder, e.Environment),
		releaseClaimRunningFor(e.StartedAt, e.Now),
		releaseRepoClaimRemote,
		releaseRepoClaimRef(e.Version))
}

// releaseClaimRunningFor reports how long the holder has been running,
// rounded to the second for a stable, readable duration. StartedAt can be
// zero for a claim record predating this field, or a previous format that
// only ever carried ExpiresAt; a zero value is reported as "an unknown
// duration" rather than the enormous, misleading span time.Since(zero) would
// compute.
func releaseClaimRunningFor(startedAt, now time.Time) string {
	if startedAt.IsZero() {
		return "an unknown duration"
	}
	return now.Sub(startedAt).Round(time.Second).String()
}

// releaseClaimHolderDescription renders a holder together with the
// environment racing it, which is the pair that actually lets a losing
// caller find their counterpart. Either half can be absent — an older
// claim record predating this field, or a holder with no other identifying
// fields set — so each is only appended when present, never leaving a
// dangling separator or an empty fragment.
func releaseClaimHolderDescription(holder EnvironmentActivityLeaseHolder, environment string) string {
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

func releaseRepoClaimRef(version string) string {
	return releaseRepoClaimRefPrefix + sanitizeForFilename(strings.TrimSpace(version))
}

func releaseRepoClaimProbeRef(version string) string {
	return releaseRepoClaimProbeRefPrefix + sanitizeForFilename(strings.TrimSpace(version))
}

// releaseRepoClaimLocalHolder answers whether the caller's own environment
// still has a live holder of the version being claimed. Only a caller in that
// same environment can answer it at all — which is why it is a probe rather
// than a field on the claim record, whose subject may live in an environment
// nothing here can see. nil means the caller cannot tell, and the record's own
// expiry is then the only rule.
type releaseRepoClaimLocalHolder func() bool

// takeReleaseRepoClaim resolves the remote claim ref's current state and
// either creates it, reclaims it from a dead or expired holder, or refuses in
// favor of a still-live one. An empty sha with a nil error means the remote
// could not be read at all (no network, no origin, no push access) —
// indistinguishable from a solo caller with nothing to contend against, so it
// is treated the same way: proceed without the repository-global claim rather
// than invent a refusal nothing confirms.
//
// localHolderAlive is consulted only for a claim the caller's own environment
// recorded, and must be read before the caller takes its own local claim: what
// it reports is whether somebody else in this environment is still holding the
// version.
func takeReleaseRepoClaim(ctx Context, projectRoot, environment, version string, holder EnvironmentActivityLeaseHolder, now time.Time, localHolderAlive releaseRepoClaimLocalHolder) (string, error) {
	ref := releaseRepoClaimRef(version)

	for attempt := 0; attempt < releaseRepoClaimMaxAttempts; attempt++ {
		existingSHA, exists, err := gitLsRemoteRef(ctx, projectRoot, releaseRepoClaimRemote, ref)
		if err != nil {
			ctx.Trace("release: could not read " + ref + " on " + releaseRepoClaimRemote + "; proceeding without the repository-global release claim")
			return "", nil
		}

		if !exists {
			// A fresh claim: nobody held this version before, so now is a
			// genuine StartedAt.
			newSHA, err := writeReleaseRepoClaimBlob(ctx, projectRoot, environment, holder, now, now)
			if err != nil {
				return "", err
			}
			if err := gitPushCreateRef(ctx, projectRoot, releaseRepoClaimRemote, newSHA, ref); err == nil {
				return newSHA, nil
			}
			continue // somebody else's create landed first; loop and re-read who holds it.
		}

		record, err := loadReleaseRepoClaimRecord(ctx, projectRoot, releaseRepoClaimRemote, ref, version)
		if err != nil {
			return "", fmt.Errorf("release: %s exists on %s but its content could not be read: %w", ref, releaseRepoClaimRemote, err)
		}
		if releaseRepoClaimHeld(record, now, environment, localHolderAlive) {
			return "", &ReleaseRepoClaimConflictError{Version: version, Holder: record.Holder, Environment: record.Environment, StartedAt: record.StartedAt, ExpiresAt: record.ExpiresAt, Now: now}
		}

		// A reclaim — of a holder whose claim lapsed, or one this environment
		// can no longer see: this is a new holder, so it gets its own
		// StartedAt rather than inheriting the dead holder's.
		newSHA, err := writeReleaseRepoClaimBlob(ctx, projectRoot, environment, holder, now, now)
		if err != nil {
			return "", err
		}
		if err := gitPushUpdateRefWithLease(ctx, projectRoot, releaseRepoClaimRemote, newSHA, ref, existingSHA); err == nil {
			return newSHA, nil
		}
		// somebody else's reclaim (or fresh claim) landed first; loop and re-read.
	}
	return "", fmt.Errorf("release: could not take the repository-global claim for %s: contended", version)
}

// renewReleaseRepoClaim extends the caller's own claim, compare-and-swapping
// against the sha it last wrote so a claim that was reclaimed out from under
// it (its own renewal having lapsed past the TTL) is never silently
// overwritten. A failure here is best-effort, matching the local lease's
// renewal: the caller keeps its last-known sha and tries again next tick.
//
// A renewal is the same holder extending the same claim, so it must carry
// the original StartedAt forward rather than resetting it to now: currentSHA
// names the blob this process itself wrote (originally or on a prior
// renewal), and that object already exists locally — reading it is a plain
// local git object lookup, not a network call, and it happens before the
// push below, so it neither reads nor writes the ref and cannot weaken the
// force-with-lease compare-and-swap that follows.
func renewReleaseRepoClaim(ctx Context, projectRoot, environment, version string, holder EnvironmentActivityLeaseHolder, now time.Time, currentSHA string) (string, error) {
	existing, err := readReleaseRepoClaimBlob(ctx, projectRoot, currentSHA)
	if err != nil {
		return "", err
	}
	newSHA, err := writeReleaseRepoClaimBlob(ctx, projectRoot, environment, holder, existing.StartedAt, now)
	if err != nil {
		return "", err
	}
	ref := releaseRepoClaimRef(version)
	if err := gitPushUpdateRefWithLease(ctx, projectRoot, releaseRepoClaimRemote, newSHA, ref, currentSHA); err != nil {
		return "", err
	}
	return newSHA, nil
}

// deleteReleaseRepoClaim drops the caller's own claim so the next release
// does not have to wait out the TTL. Idempotent by construction: the
// force-with-lease only removes the ref if it still holds the caller's own
// sha, so a claim already reclaimed by someone else (this caller's TTL having
// lapsed) is left alone rather than deleting a legitimate new holder.
func deleteReleaseRepoClaim(ctx Context, projectRoot, version, currentSHA string) error {
	ref := releaseRepoClaimRef(version)
	lease := fmt.Sprintf("--force-with-lease=%s:%s", ref, currentSHA)
	ctx.TraceCommand(projectRoot, "git", "push", lease, releaseRepoClaimRemote, "--delete", ref)
	return Command("git", "-C", projectRoot, "push", lease, releaseRepoClaimRemote, "--delete", ref).Run()
}

// releaseRepoClaimHeld reports whether record still stands in the way of a
// caller releasing version from environment.
//
// Its expiry is not the whole answer for a claim this same environment
// recorded. A holder that is cancelled, crashes, or loses its pod never runs
// the deferred release that deletes the claim, so the claim outlived the
// holder it names and refused every retry for the rest of its twenty-minute
// TTL — while the refusal itself promised to reclaim exactly those holders on
// the next attempt. A pid cannot fix that: it names a process in one pod of
// one environment, and the claim is deliberately readable from every
// environment that can push to the release's origin.
//
// The liveness that is observable is the environment's own: every release also
// takes a local exclusive lease for the same version, keyed to the process
// holding it and reclaimed the moment that process is gone. So for a claim
// recorded against the caller's own environment, that local store answers "is
// the holder still there", and the claim is reclaimable as soon as it is not.
// A claim recorded against any other environment keeps the expiry-only rule,
// because nothing here can see whether that holder is alive. A record with no
// environment at all (one written before that field existed) keeps it too,
// rather than being treated as the caller's own.
func releaseRepoClaimHeld(record releaseRepoClaimRecord, now time.Time, environment string, localHolderAlive releaseRepoClaimLocalHolder) bool {
	if record.ExpiresAt.IsZero() || !now.Before(record.ExpiresAt) {
		return false
	}
	if environment == "" || record.Environment != environment || localHolderAlive == nil {
		return true
	}
	return localHolderAlive()
}

func writeReleaseRepoClaimBlob(ctx Context, projectRoot, environment string, holder EnvironmentActivityLeaseHolder, startedAt, now time.Time) (string, error) {
	record := releaseRepoClaimRecord{
		Holder:      holder,
		Environment: environment,
		StartedAt:   startedAt,
		ExpiresAt:   now.Add(releaseVersionClaimTTL),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	ctx.TraceCommand(projectRoot, "git", "hash-object", "-w", "--stdin")
	cmd := Command("git", "-C", projectRoot, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(string(data))
	output, err := cmd.Output()
	if err != nil {
		if stderr := stderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// readReleaseRepoClaimBlob reads a claim record blob this process itself
// wrote earlier via writeReleaseRepoClaimBlob, by its git object sha. The
// object is already present in the local object database — no fetch, no ref
// read — which is what lets the renewal path recover the original StartedAt
// without touching the CAS in takeReleaseRepoClaim/renewReleaseRepoClaim.
func readReleaseRepoClaimBlob(ctx Context, projectRoot, sha string) (releaseRepoClaimRecord, error) {
	ctx.TraceCommand(projectRoot, "git", "cat-file", "-p", sha)
	output, err := Command("git", "-C", projectRoot, "cat-file", "-p", sha).Output()
	if err != nil {
		return releaseRepoClaimRecord{}, err
	}
	var record releaseRepoClaimRecord
	if err := json.Unmarshal(output, &record); err != nil {
		return releaseRepoClaimRecord{}, err
	}
	return record, nil
}

// gitLsRemoteRef reads a single ref's current sha directly from the remote,
// with no local repo state written — safe to call while the release's own
// git commands run concurrently in the same checkout. ok is false both when
// the ref does not exist and when the read failed outright; the caller tells
// these apart via err.
func gitLsRemoteRef(ctx Context, projectRoot, remote, ref string) (sha string, ok bool, err error) {
	ctx.TraceCommand(projectRoot, "git", "ls-remote", remote, ref)
	output, err := Command("git", "-C", projectRoot, "ls-remote", remote, ref).Output()
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

// loadReleaseRepoClaimRecord fetches ref's current object into a throwaway
// local probe ref (never FETCH_HEAD — see releaseRepoClaimProbeRefPrefix)
// and reads its content back, cleaning up the probe ref afterward regardless
// of outcome.
func loadReleaseRepoClaimRecord(ctx Context, projectRoot, remote, ref, version string) (releaseRepoClaimRecord, error) {
	probe := releaseRepoClaimProbeRef(version)
	defer func() {
		_ = Command("git", "-C", projectRoot, "update-ref", "-d", probe).Run()
	}()

	fetchSpec := "+" + ref + ":" + probe
	ctx.TraceCommand(projectRoot, "git", "fetch", "--no-write-fetch-head", remote, fetchSpec)
	if err := Command("git", "-C", projectRoot, "fetch", "--no-write-fetch-head", remote, fetchSpec).Run(); err != nil {
		return releaseRepoClaimRecord{}, err
	}
	output, err := Command("git", "-C", projectRoot, "cat-file", "-p", probe).Output()
	if err != nil {
		if stderr := stderrFromExitError(err); stderr != "" {
			return releaseRepoClaimRecord{}, fmt.Errorf("%w: %s", err, stderr)
		}
		return releaseRepoClaimRecord{}, err
	}
	var record releaseRepoClaimRecord
	if err := json.Unmarshal(output, &record); err != nil {
		return releaseRepoClaimRecord{}, err
	}
	return record, nil
}

func gitPushCreateRef(ctx Context, projectRoot, remote, sha, ref string) error {
	ctx.TraceCommand(projectRoot, "git", "push", remote, sha+":"+ref)
	return Command("git", "-C", projectRoot, "push", remote, sha+":"+ref).Run()
}

func gitPushUpdateRefWithLease(ctx Context, projectRoot, remote, sha, ref, expectedSHA string) error {
	lease := fmt.Sprintf("--force-with-lease=%s:%s", ref, expectedSHA)
	ctx.TraceCommand(projectRoot, "git", "push", lease, remote, sha+":"+ref)
	return Command("git", "-C", projectRoot, "push", lease, remote, sha+":"+ref).Run()
}
