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

// jobReportStore's zero value is an install with no platform alias.
type jobReportStore struct{ config configpkg.ERunConfig }

func (s jobReportStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

// erunAliasStore has an erun alias with no usable API URL.
func erunAliasStore() jobReportStore {
	return jobReportStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{
		{Alias: "erun", Provider: configpkg.CloudProviderERun, ERun: &configpkg.ERunProviderConfig{}},
	}}}
}

func traceBuffer() (core.Context, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, buf, buf)}, buf
}

// No alias must mean no trace and no network at all.
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

// Any other conflict must not read as a held scope.
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
