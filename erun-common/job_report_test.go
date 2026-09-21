package eruncommon

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// jobReportStore is the config store the reporting contract is exercised
// against. job_record.go reads it only to decide whether a platform alias
// exists at all, so the zero value is the case that matters most: an install
// that has never configured one.
type jobReportStore struct{ config ERunConfig }

func (s jobReportStore) LoadERunConfig() (ERunConfig, string, error) {
	return s.config, "", nil
}

// erunAliasStore is the other half: an install that has an erun-type alias
// configured but no usable API URL on it (an interrupted login), which is the
// first thing that can go wrong after the alias check.
func erunAliasStore() jobReportStore {
	return jobReportStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{
		{Alias: "erun", Provider: CloudProviderERun, ERun: &ERunProviderConfig{}},
	}}}
}

// traceBuffer wires a Context whose trace lines are captured rather than
// printed, so a test can assert on what a caller would actually see.
func traceBuffer() (Context, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return Context{Logger: NewLoggerWithWriters(VerbosityInfo, buf, buf)}, buf
}

// TestReportJobStartWithoutAnAliasSaysNothingAtAll is the contract's sharpest
// edge: an install with no platform alias configured -- the overwhelming
// majority of invocations -- must behave exactly as it did before job
// reporting existed. Not merely "not fail": say nothing, trace nothing, and
// touch no network. A single trace line here would be a behaviour change on
// every agent run in every unconfigured environment.
func TestReportJobStartWithoutAnAliasSaysNothingAtAll(t *testing.T) {
	ctx, trace := traceBuffer()

	jobID := ReportJobStart(ctx, jobReportStore{}, CloudDependencies{}, ReportJobStartParams{
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

	jobID := ReportJobStart(ctx, erunAliasStore(), CloudDependencies{}, ReportJobStartParams{
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

	ReportJobOutcome(ctx, erunAliasStore(), CloudDependencies{}, ReportJobOutcomeParams{
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

	ReportJobOutcome(ctx, jobReportStore{}, CloudDependencies{}, ReportJobOutcomeParams{
		JobID:  "job-1",
		Status: "SUCCEEDED",
	})

	if trace.String() != "" {
		t.Fatalf("trace = %q, want silence with nothing configured to report to", trace.String())
	}
}

func jobStatusError(t *testing.T, status int, body string) error {
	t.Helper()
	return platformStatusError(http.MethodPost, "/v1/jobs", status, []byte(body), http.Header{})
}

// TestPlatformJobScopeHeldDetailsNamesTheHolder: the refusal's whole value is
// that a caller is told who holds the scope, what they are doing in prose, and
// since when. A caller that can only read "conflict" has to guess whether to
// wait or to duplicate the work.
func TestPlatformJobScopeHeldDetailsNamesTheHolder(t *testing.T) {
	err := jobStatusError(t, http.StatusConflict, `{"code":"JOB_SCOPE_HELD","message":"scope held","details":{
		"scope":"sophium/erun#2109","jobId":"job-held","actorId":"erun/code4",
		"summary":"fixing the jobs claim race","startedAt":"2026-09-21T12:00:00Z"}}`)

	held, ok := PlatformJobScopeHeldDetails(err)
	if !ok {
		t.Fatal("a JOB_SCOPE_HELD conflict was not recognised")
	}
	if held.Scope != "sophium/erun#2109" {
		t.Errorf("scope = %q, want sophium/erun#2109", held.Scope)
	}
	if held.ActorID != "erun/code4" {
		t.Errorf("actorId = %q, want erun/code4", held.ActorID)
	}
	if held.Summary != "fixing the jobs claim race" {
		t.Errorf("summary = %q, want the holder's prose", held.Summary)
	}
	if held.StartedAt != "2026-09-21T12:00:00Z" {
		t.Errorf("startedAt = %q, want the holder's start time", held.StartedAt)
	}
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
		if _, ok := PlatformJobScopeHeldDetails(err); ok {
			t.Errorf("%s: reported as a held scope", name)
		}
	}
}

// TestJobReportScopeHeldIsTracedAsWhatItIs: when the only thing that happened
// is that another actor already holds the scope, the trace must name that
// holder rather than reporting another opaque failure -- it is the one skip
// with a remedy a reader can act on.
func TestJobReportScopeHeldIsTracedAsWhatItIs(t *testing.T) {
	err := jobStatusError(t, http.StatusConflict, `{"code":"JOB_SCOPE_HELD","message":"scope held","details":{
		"scope":"sophium/erun#2109","jobId":"job-held","actorId":"erun/code4",
		"summary":"fixing the jobs claim race","startedAt":"2026-09-21T12:00:00Z"}}`)

	held, ok := PlatformJobScopeHeldDetails(err)
	if !ok {
		t.Fatal("precondition: the conflict should resolve to a holder")
	}
	if !strings.Contains(held.ActorID, "erun/code4") || !strings.Contains(held.Scope, "sophium/erun#2109") {
		t.Fatalf("holder payload = %+v, want the scope and the actor", held)
	}
}
