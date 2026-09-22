package eruncommon

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// isTransientRegistryReadError decides between "retry the read-back" and "abort
// the publish" by matching the read-back's text, so its only damaging failure
// mode is under-matching: a wording it stops recognising aborts a release
// immediately on a read the registry would have answered a moment later. These
// tables pin the wordings erun has evidence for, and each row records where its
// output came from so a later reader can tell an observed registry answer from a
// reconstructed one. Adding a marker to the classifier means adding a row here.
type registryReadSample struct {
	output string
	source string
}

// transientRegistryReadSamples covers every marker in the classifier with the
// shape of output helm, docker, or their registries produce on a read-back of an
// artifact erun has just pushed.
func transientRegistryReadSamples() []registryReadSample {
	return []registryReadSample{
		{
			output: `Error: failed to fetch https://ghcr.io/v2/acme/charts/erun-runtime/manifests/1.0.248: 401 Unauthorized`,
			source: "constructed: the standard registry 401 body, matching the authorization shape the classifier documents",
		},
		{
			output: `Error: failed to authorize: failed to fetch anonymous token: unexpected status from GET request to https://ghcr.io/token?scope=repository%3Aacme%2Fcharts%3Apull&service=ghcr.io: 403 Forbidden`,
			source: "reconstructed from the source comment: the registry mints the pull token before the pushed tag is listed and answers the first fetch 403 denied",
		},
		{
			output: `Error: failed to fetch https://ghcr.io/v2/acme/charts/erun-runtime/manifests/1.0.248: 404 Not Found`,
			source: "constructed: the same read-after-write race surfacing as a not-yet-listed tag",
		},
		{
			output: `Error: denied: requested access to the resource is denied`,
			source: "constructed: the registry's own denial line, the wording the source comment records as 'denied'",
		},
		{
			output: `Error: manifest unknown`,
			source: "observed registry wording for a tag the backend has not listed yet",
		},
		{
			output: `Error: chart "erun-runtime" version "1.0.248" not found in repo`,
			source: "quoted when this coverage was requested, as the deliberately-transient case: a just-pushed chart can read back missing before it propagates",
		},
		{
			output: `Error: Get "https://ghcr.io/v2/acme/charts/erun-runtime/manifests/1.0.248": net/http: request canceled (Client.Timeout exceeded while awaiting headers)`,
			source: "constructed: a Go HTTP client timeout, the transport-failure shape the classifier documents",
		},
		{
			output: `Error: failed to download chart: connection timed out`,
			source: "constructed: the resolver-independent 'timed out' phrasing",
		},
		{
			output: `Error: Get "https://ghcr.io/v2/": dial tcp: lookup ghcr.io on 127.0.0.53:53: Temporary failure in name resolution`,
			source: "observed glibc resolver wording, reachable from helm inside a build pod",
		},
		{
			output: `Error: Get "https://ghcr.io/v2/acme/charts/erun-runtime/manifests/1.0.248": read tcp 10.0.0.5:54321->140.82.121.34:443: connection reset by peer`,
			source: "constructed: the connection-reset shape a proxy in the path produces",
		},
		{
			output: `Error: Get "http://127.0.0.1:5000/v2/": dial tcp 127.0.0.1:5000: connect: connection refused`,
			source: "constructed: a local or insecure registry not yet accepting connections",
		},
		{
			output: `Error: Get "https://ghcr.io/v2/acme/charts/erun-runtime/manifests/1.0.248": unexpected EOF`,
			source: "constructed: a truncated response, the wording the bare 'eof' marker exists for",
		},
		{
			output: `Error: Get "https://ghcr.io/v2/acme/charts/erun-runtime/manifests/1.0.248": dial tcp: lookup ghcr.io: no such host`,
			source: "observed Go resolver wording for a DNS answer that has not landed",
		},
		{
			output: `Error: Get "https://ghcr.io/v2/acme/charts/erun-runtime/manifests/1.0.248": net/http: TLS handshake timeout`,
			source: "constructed: the TLS handshake wording a stalled registry connection produces",
		},
		{
			output: `Error: received unexpected HTTP status: 503 Service Unavailable`,
			source: "observed registry answer while the backend sheds load",
		},
		{
			output: `Error: received unexpected HTTP status: 429 Too Many Requests`,
			source: "observed registry rate-limit answer during a burst of read-backs",
		},
		{
			output: `Error: received unexpected HTTP status: 500 Internal Server Error`,
			source: "observed registry answer with a reason phrase following the status code",
		},
		{
			output: `Error: received unexpected HTTP status: 502 Bad Gateway`,
			source: "observed proxy answer in front of the registry",
		},
		{
			output: `Error: received unexpected HTTP status: 500`,
			source: "constructed: the same status with no reason phrase, the form the previous trailing-space marker failed to match while its 502 and 503 siblings matched theirs",
		},
		{
			output: `Error: received unexpected HTTP status: 502`,
			source: "constructed: the reason-phrase-less form, pinned so all three status codes classify alike",
		},
		{
			output: `Error: received unexpected HTTP status: 503`,
			source: "constructed: the reason-phrase-less form, pinned so all three status codes classify alike",
		},
	}
}

// finalRegistryReadSamples are failures that say nothing about propagation: the
// artifact is malformed, misnamed, or the invocation itself is wrong. Retrying
// one of these costs three seconds and reaches the same verdict, so the retry
// must not engage.
func finalRegistryReadSamples() []registryReadSample {
	return []registryReadSample{
		{
			output: `Error: cannot load Chart.yaml: error converting YAML to JSON: yaml: line 3: mapping values are not allowed in this context`,
			source: "constructed: a malformed chart, the final shape named when this coverage was requested",
		},
		{
			output: `Error: file "erun-runtime-1.0.248.tgz" does not match the expected digest`,
			source: "constructed: a checksum failure, the other final shape named when this coverage was requested",
		},
		{
			output: `Error: chart metadata is invalid: name is required`,
			source: "constructed: a chart the registry will never serve, however long it propagates",
		},
		{
			output: `Error: unknown flag: --version`,
			source: "constructed: erun's own invocation is wrong, so no registry answer is involved",
		},
		{
			output: `Error: execution error at (erun-runtime/templates/deployment.yaml:12:14): value for key "image" is required`,
			source: "constructed: a template error raised while rendering the fetched chart",
		},
		{
			output: ``,
			source: "constructed: a silent nonzero exit leaves no text to classify, so the read cannot be assumed transient",
		},
	}
}

func TestIsTransientRegistryReadErrorMatchesEveryMarker(t *testing.T) {
	// The classifier's marker list as this test last saw it. Duplicating the
	// literals is deliberate: it fails if a marker is dropped, which is the
	// under-matching direction that aborts a release. A marker added to the
	// classifier needs a realistic row in transientRegistryReadSamples instead.
	markers := []string{
		"401", "403", "404",
		"denied", "unauthorized", "not found", "manifest unknown",
		"timeout", "timed out", "temporary failure", "connection reset",
		"connection refused", "eof", "no such host", "tls handshake",
		"service unavailable", "too many requests", "500", "502", "503",
	}
	for _, marker := range markers {
		if !isTransientRegistryReadError(marker) {
			t.Errorf("marker %q does not classify as transient; a registry answer carrying it would abort the publish", marker)
		}
	}
}

func TestIsTransientRegistryReadErrorClassifiesRealisticRegistryOutput(t *testing.T) {
	for _, sample := range transientRegistryReadSamples() {
		if !isTransientRegistryReadError(sample.output) {
			t.Errorf("output %q (%s) classified as final; this is the under-match that aborts a release", sample.output, sample.source)
		}
	}
}

func TestIsTransientRegistryReadErrorTreatsFinalFailuresAsFinal(t *testing.T) {
	for _, sample := range finalRegistryReadSamples() {
		if isTransientRegistryReadError(sample.output) {
			t.Errorf("output %q (%s) classified as transient; retrying it only delays the same verdict", sample.output, sample.source)
		}
	}
}

// captureRegistryReadLog points both trace writers at one buffer: the retry
// announcement is what tells an operator the publish is waiting rather than
// wedged, so the loop under test must be readable from the Context it was given.
func captureRegistryReadLog() (Context, *bytes.Buffer) {
	log := &bytes.Buffer{}
	return Context{Logger: NewLoggerWithWriters(VerbosityInfo, log, log)}, log
}

const (
	transientReadBackOutput = `Error: failed to authorize: failed to fetch anonymous token: unexpected status from GET request to https://ghcr.io/token: 403 Forbidden`
	finalReadBackOutput     = `Error: cannot load Chart.yaml: error converting YAML to JSON: yaml: line 3: mapping values are not allowed in this context`
	readBackSubject         = "chart erun-runtime 1.0.248"
)

func TestReadBackPublishedArtifactRetriesTransientReadsUpToTheBound(t *testing.T) {
	ctx, log := captureRegistryReadLog()
	lastErr := errors.New("exit status 1")
	attempts := 0

	err := readBackPublishedArtifact(ctx, readBackSubject, func() (string, error) {
		attempts++
		return transientReadBackOutput, lastErr
	})

	if attempts != registryVerifyMaxAttempts {
		t.Fatalf("attempts = %d, want the bounded %d: a read-back that never succeeds must fail instead of retrying forever", attempts, registryVerifyMaxAttempts)
	}
	if !errors.Is(err, lastErr) {
		t.Fatalf("err = %v, want the last read's own error, not a generic failure", err)
	}
	for _, want := range []string{
		"retrying in 500ms (attempt 2 of 4)",
		"retrying in 1s (attempt 3 of 4)",
		"retrying in 1.5s (attempt 4 of 4)",
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("retry announcement missing %q; a retry the operator cannot see is indistinguishable from a hang\ngot:\n%s", want, log.String())
		}
	}
}

func TestReadBackPublishedArtifactDoesNotRetryAFinalFailure(t *testing.T) {
	ctx, log := captureRegistryReadLog()
	lastErr := errors.New("exit status 1")
	attempts := 0

	err := readBackPublishedArtifact(ctx, readBackSubject, func() (string, error) {
		attempts++
		return finalReadBackOutput, lastErr
	})

	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1: a non-transient failure must not be retried at all", attempts)
	}
	if !errors.Is(err, lastErr) {
		t.Fatalf("err = %v, want the read's own error", err)
	}
	if strings.Contains(log.String(), "retrying") {
		t.Errorf("a final failure announced a retry:\n%s", log.String())
	}
}

func TestReadBackPublishedArtifactStopsRetryingOnceTheReadSucceeds(t *testing.T) {
	ctx, log := captureRegistryReadLog()
	attempts := 0

	err := readBackPublishedArtifact(ctx, readBackSubject, func() (string, error) {
		attempts++
		if attempts == 1 {
			return transientReadBackOutput, errors.New("exit status 1")
		}
		return "", nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil once the read succeeds within the bound", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2: the loop must stop as soon as the read succeeds", attempts)
	}
	if !strings.Contains(log.String(), "retrying in 500ms (attempt 2 of 4)") {
		t.Errorf("the successful retry was not announced:\n%s", log.String())
	}
}
