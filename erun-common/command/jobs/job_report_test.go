package jobs

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// jobReportStore is the config store the reporting contract is exercised
// against. job_record.go reads it only to decide whether a platform alias
// exists at all, so the zero value is the case that matters most: an install
// that has never configured one.
type jobReportStore struct{ config configpkg.ERunConfig }

func (s jobReportStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

// erunAliasStore is the other half: an install that has an erun-type alias
// configured but no usable API URL on it (an interrupted login), which is the
// first thing that can go wrong after the alias check.
func erunAliasStore() jobReportStore {
	return jobReportStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{
		{Alias: "erun", Provider: configpkg.CloudProviderERun, ERun: &configpkg.ERunProviderConfig{}},
	}}}
}

// traceBuffer wires a Context whose trace lines are captured rather than
// printed, so a test can assert on what a caller would actually see.
func traceBuffer() (core.Context, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, buf, buf)}, buf
}

// TestReportJobStartWithoutAnAliasSaysNothingAtAll is the contract's sharpest
// edge: an install with no platform alias configured -- the overwhelming
// majority of invocations -- must behave exactly as it did before job
// reporting existed. Not merely "not fail": say nothing, trace nothing, and
// touch no network. A single trace line here would be a behaviour change on
// every agent run in every unconfigured environment.
func TestReportJobStartWithoutAnAliasSaysNothingAtAll(t *testing.T) {
	ctx, trace := traceBuffer()

	jobID := ReportJobStart(ctx, jobReportStore{}, cloud.CloudDependencies{}, ReportJobStartParams{
		JobType:   "fix",
		Summary:   "fixing the jobs claim race",
		ActorKind: "agent",
		ActorID:   "erun/code4",
	})

	if jobID != "" {
		t.Fatalf("jobID = %q, want \"\" with nothing configured to report to", jobID)
	}
	if trace.String() != "" {
		t.Fatalf("an unconfigured install traced %q; the contract is complete silence, not a skip notice", trace.String())
	}
}

// TestReportJobStartWithAnUnusableAliasTracesAndKeepsGoing: every failure
// after the alias check is a recorded skip -- traced, never silent, never
// propagated as a failure of the work being recorded. A caller has no error
// to handle because there is none to give.
func TestReportJobStartWithAnUnusableAliasTracesAndKeepsGoing(t *testing.T) {
	ctx, trace := traceBuffer()

	jobID := ReportJobStart(ctx, erunAliasStore(), cloud.CloudDependencies{}, ReportJobStartParams{
		JobType:   "fix",
		Summary:   "fixing the jobs claim race",
		ActorKind: "agent",
		ActorID:   "erun/code4",
	})

	if jobID != "" {
		t.Fatalf("jobID = %q, want \"\" when the alias cannot be used", jobID)
	}
	if !strings.Contains(trace.String(), "job record to erun platform skipped:") {
		t.Fatalf("trace = %q, want a recorded skip naming the reason", trace.String())
	}
}

// TestReportJobOutcomeWithoutAJobIDSaysNothing: an empty job id means the
// start was never recorded, so there is nothing to close. The caller that had
// no id already saw why, and repeating it here would double every skip notice.
func TestReportJobOutcomeWithoutAJobIDSaysNothing(t *testing.T) {
	ctx, trace := traceBuffer()

	ReportJobOutcome(ctx, erunAliasStore(), cloud.CloudDependencies{}, ReportJobOutcomeParams{
		JobID:  "   ",
		Status: "SUCCEEDED",
	})

	if trace.String() != "" {
		t.Fatalf("trace = %q, want silence when there is no job to close", trace.String())
	}
}

// TestReportJobOutcomeWithoutAnAliasSaysNothingAtAll is the same silent-no-op
// contract for the closing half: a caller that recorded a job on one machine
// and closes it on another has no alias there, and must not be told about it.
func TestReportJobOutcomeWithoutAnAliasSaysNothingAtAll(t *testing.T) {
	ctx, trace := traceBuffer()

	ReportJobOutcome(ctx, jobReportStore{}, cloud.CloudDependencies{}, ReportJobOutcomeParams{
		JobID:  "job-1",
		Status: "SUCCEEDED",
	})

	if trace.String() != "" {
		t.Fatalf("trace = %q, want silence with nothing configured to report to", trace.String())
	}
}

func jobStatusError(t *testing.T, status int, body string) error {
	t.Helper()
	return platformclient.NewPlatformStatusError(http.MethodPost, "/v1/jobs", status, []byte(body), http.Header{})
}

// TestPlatformJobScopeHeldDetailsRefusesAnyOtherConflict: a conflict that is
// not a held scope -- an already-finished job, say -- must not be reported as
// one. A caller told "someone else has this" when they in fact own it would
// abandon work that is still theirs to finish.
func TestPlatformJobScopeHeldDetailsRefusesAnyOtherConflict(t *testing.T) {
	for name, err := range map[string]error{
		"another conflict code": jobStatusError(t, http.StatusConflict, `{"code":"JOB_ALREADY_FINISHED","message":"finished"}`),
		"a different status":    jobStatusError(t, http.StatusBadRequest, `{"code":"JOB_SCOPE_HELD"}`),
		"an unparseable body":   jobStatusError(t, http.StatusConflict, `not json`),
		"not a status error":    errors.New("connection reset"),
	} {
		if _, ok := platformclient.PlatformJobScopeHeldDetails(err); ok {
			t.Errorf("%s: reported as a held scope", name)
		}
	}
}
