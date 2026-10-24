package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func requireLoneRestartPreviewNotice(t *testing.T, preview []model.DesktopRestartReopen, id string, wants ...string) {
	t.Helper()
	if len(preview) != 1 || preview[0].OrchestratorID != id {
		t.Fatalf("expected one row for %s, got %+v", id, preview)
	}
	for _, want := range wants {
		if !strings.Contains(preview[0].Notice, want) {
			t.Fatalf("expected the row's notice to carry %q, got %q", want, preview[0].Notice)
		}
	}
}

func TestARestartPreviewWarnsWhenTheNextLaunchWouldBeRefused(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	var credentialErr error
	app.deps.resolveGatewayCredential = func(*config.OpenRouterConfig) (string, error) {
		return testGatewayToken, credentialErr
	}
	stageGatewayCatalog(app)
	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{UseGateway: true})
	mustNoErr(t, err, "CreateOrchestrator")
	_, err = app.StartOrchestrator(created.ID, 80, 24)
	mustNoErr(t, err, "StartOrchestrator")
	id := created.ID

	preview := app.orchestratorRestartPreview(id)
	if len(preview) != 1 || preview[0].OrchestratorID != id || preview[0].Notice != "" {
		t.Fatalf("expected the orchestrator reopened with nothing to report, got %+v", preview)
	}

	credentialErr = errors.New("no gateway credential: nothing is saved under \"claude-gateway\"")
	requireLoneRestartPreviewNotice(t, app.orchestratorRestartPreview(id), id,
		"The restart will not reopen "+id,
		"no gateway credential",
		"save the gateway key in the desktop's ERun settings",
		"Its background agent keeps running")
	if entries, err := os.ReadDir(restoreDir); err == nil && len(entries) != 0 {
		t.Fatalf("a preview must write no hand-off, found %d", len(entries))
	}

	credentialErr = nil
	stubStore(app).config.OpenRouter = nil
	requireLoneRestartPreviewNotice(t, app.orchestratorRestartPreview(id), id,
		"no gateway catalog is configured", "configure the gateway catalog")
}
