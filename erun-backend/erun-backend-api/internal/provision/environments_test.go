package provision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// stubImageChecker reports a fixed answer, or an error when set.
type stubImageChecker struct {
	missing    bool
	err        error
	calls      int
	gotImage   string
	gotControl string
	// present/presentErr answer ConfirmedPresent, tracked separately from the
	// ConfirmedMissing fields above since a caller may probe both in one run
	// (see reportRuntimeImageFallbackSubstitution).
	present           bool
	presentErr        error
	presentCalls      int
	presentGotImage   string
	presentGotControl string
}

func (c *stubImageChecker) ConfirmedMissing(_ context.Context, image, control string) (bool, error) {
	c.calls++
	c.gotImage = image
	c.gotControl = control
	return c.missing, c.err
}

func (c *stubImageChecker) ConfirmedPresent(_ context.Context, image, control string) (bool, error) {
	c.presentCalls++
	c.presentGotImage = image
	c.presentGotControl = control
	return c.present, c.presentErr
}

// TestResolveBootstrapImage locks the synchronous precondition Start/StartDeploy
// run before enqueueing the durable workflow: only a registry-confirmed
// absence selects bootstrap, matching RuntimeImageChecker's fail-open contract.
func TestResolveBootstrapImage(t *testing.T) {
	config := EnvDeployConfig{Registry: "ghcr.io/sophium"}
	input := EnvProvisionInput{Tenant: "acme", Version: "1.2.3"}

	t.Run("nil checker never bootstraps", func(t *testing.T) {
		p := &EnvProvisioner{config: config}
		if p.resolveBootstrapImage(input) {
			t.Fatal("resolveBootstrapImage = true, want false with no checker wired")
		}
	})

	t.Run("confirmed missing selects bootstrap", func(t *testing.T) {
		checker := &stubImageChecker{missing: true}
		p := &EnvProvisioner{config: config, imageChecker: checker}
		if !p.resolveBootstrapImage(input) {
			t.Fatal("resolveBootstrapImage = false, want true on a confirmed-missing tenant image")
		}
		if checker.gotImage != "ghcr.io/sophium/acme-devops:1.2.3" {
			t.Fatalf("checker probed %q", checker.gotImage)
		}
		// The control reference is the image the bootstrap itself would run, so
		// the probe's credential is proven against something this deploy already
		// depends on resolving.
		if checker.gotControl != "ghcr.io/sophium/erun-devops:1.2.3" {
			t.Fatalf("checker control reference = %q, want the canonical ghcr.io/sophium/erun-devops:1.2.3", checker.gotControl)
		}
	})

	t.Run("confirmed present does not bootstrap", func(t *testing.T) {
		p := &EnvProvisioner{config: config, imageChecker: &stubImageChecker{missing: false}}
		if p.resolveBootstrapImage(input) {
			t.Fatal("resolveBootstrapImage = true, want false when the tenant's own image exists")
		}
	})

	t.Run("checker error does not bootstrap", func(t *testing.T) {
		p := &EnvProvisioner{config: config, imageChecker: &stubImageChecker{missing: true, err: errors.New("network down")}}
		if p.resolveBootstrapImage(input) {
			t.Fatal("resolveBootstrapImage = true, want false (fail open on checker error)")
		}
	})
}

// TestReportRuntimeImageFallbackSubstitutionReportsAConfirmedSubstitution
// locks that the runtime-image fallback names the substitution only when a
// differently-named image the platform actually publishes is affirmatively
// confirmed present; the sibling test below locks every case where it must
// stay silent instead.
func TestReportRuntimeImageFallbackSubstitutionReportsAConfirmedSubstitution(t *testing.T) {
	config := EnvDeployConfig{Registry: "ghcr.io/sophium", PlatformTenant: "frs"}
	operationsInput := EnvProvisionInput{Tenant: "operations", TenantType: string(model.TenantTypeOperations), Version: "1.2.3"}
	checker := &stubImageChecker{missing: true, present: true}
	p := &EnvProvisioner{config: config, imageChecker: checker}
	output := captureLog(t, func() {
		if !p.resolveBootstrapImage(operationsInput) {
			t.Fatal("resolveBootstrapImage = false, want true on a confirmed-missing tenant image")
		}
	})
	if checker.presentGotImage != "ghcr.io/sophium/frs-devops:1.2.3" {
		t.Fatalf("checker probed presence of %q, want the declared tenant's own image", checker.presentGotImage)
	}
	if checker.presentGotControl != "ghcr.io/sophium/erun-devops:1.2.3" {
		t.Fatalf("checker probed presence against control %q, want the canonical image", checker.presentGotControl)
	}
	for _, want := range []string{"operations", "frs", "ghcr.io/sophium/frs-devops:1.2.3", "ghcr.io/sophium/erun-devops:1.2.3", "PATCH /v1/tenants/reconcile-bootstrap-name"} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output = %q, want it to contain %q", output, want)
		}
	}
}

// assertNoSubstitutionReported drives resolveBootstrapImage and asserts the
// fallback substitution log line never fires, so each silence scenario below
// reads as the precondition it violates plus the one assertion that matters.
func assertNoSubstitutionReported(t *testing.T, p *EnvProvisioner, input EnvProvisionInput, reason string) {
	t.Helper()
	output := captureLog(t, func() { p.resolveBootstrapImage(input) })
	if strings.Contains(output, "reconcile-bootstrap-name") {
		t.Fatalf("log output = %q, want no reported substitution: %s", output, reason)
	}
}

// TestReportRuntimeImageFallbackSubstitutionStaysSilent covers every
// precondition that must suppress the report: an unconfirmed probe, a
// non-operations tenant, no declared platform tenant, and a tenant already
// named the declared tenant.
func TestReportRuntimeImageFallbackSubstitutionStaysSilent(t *testing.T) {
	config := EnvDeployConfig{Registry: "ghcr.io/sophium", PlatformTenant: "frs"}
	operationsInput := EnvProvisionInput{Tenant: "operations", TenantType: string(model.TenantTypeOperations), Version: "1.2.3"}

	t.Run("declared name's image not confirmed present", func(t *testing.T) {
		p := &EnvProvisioner{config: config, imageChecker: &stubImageChecker{missing: true, present: false}}
		assertNoSubstitutionReported(t, p, operationsInput, "an unconfirmed probe proves nothing")
	})

	t.Run("ordinary (non-operations) tenant", func(t *testing.T) {
		checker := &stubImageChecker{missing: true, present: true}
		p := &EnvProvisioner{config: config, imageChecker: checker}
		companyInput := EnvProvisionInput{Tenant: "acme", TenantType: string(model.TenantTypeCompany), Version: "1.2.3"}
		assertNoSubstitutionReported(t, p, companyInput, "an ordinary tenant has no other declared name to check")
		if checker.presentCalls != 0 {
			t.Fatalf("presentCalls = %d, want 0: an ordinary tenant has no other declared name to check", checker.presentCalls)
		}
	})

	t.Run("no ERUN_TENANT configured", func(t *testing.T) {
		p := &EnvProvisioner{config: EnvDeployConfig{Registry: "ghcr.io/sophium"}, imageChecker: &stubImageChecker{missing: true, present: true}}
		assertNoSubstitutionReported(t, p, operationsInput, "nothing is declared to compare against")
	})

	t.Run("tenant already named the declared tenant", func(t *testing.T) {
		p := &EnvProvisioner{config: config, imageChecker: &stubImageChecker{missing: true, present: true}}
		alreadyNamed := EnvProvisionInput{Tenant: "frs", TenantType: string(model.TenantTypeOperations), Version: "1.2.3"}
		assertNoSubstitutionReported(t, p, alreadyNamed, "the tenant already matches")
	})
}

// TestNamespaceQuotaQuantityConversion locks the millicore/MiB/GiB -> Kubernetes
// quantity string rendering deployJobParams uses, and that zero (no cap
// configured) renders empty rather than "0m"/"0Mi"/"0Gi".
func TestNamespaceQuotaQuantityConversion(t *testing.T) {
	params := deployJobParams(
		EnvDeployConfig{Registry: "ghcr.io/sophium", PlatformNamespace: "erun-prod", DeployerServiceAccount: "erun-env-deployer"},
		EnvProvisionInput{Tenant: "acme", Environment: "prod", Version: "1.2.3", MaxCPUMillicores: 4000, MaxMemoryMB: 9216, MaxStorageGB: 80},
	)
	if params.MaxCPU != "4000m" || params.MaxMemory != "9216Mi" || params.MaxStorage != "80Gi" {
		t.Fatalf("quota quantities = %q/%q/%q, want 4000m/9216Mi/80Gi", params.MaxCPU, params.MaxMemory, params.MaxStorage)
	}
	if params.MaxCPUMillicores != 4000 || params.MaxMemoryMB != 9216 || params.MaxStorageGB != 80 {
		t.Fatalf("raw quota ints = %d/%d/%d, want 4000/9216/80", params.MaxCPUMillicores, params.MaxMemoryMB, params.MaxStorageGB)
	}

	zero := deployJobParams(EnvDeployConfig{}, EnvProvisionInput{Tenant: "acme", Environment: "prod", Version: "1.2.3"})
	if zero.MaxCPU != "" || zero.MaxMemory != "" || zero.MaxStorage != "" {
		t.Fatalf("zero-cap quantities = %q/%q/%q, want empty", zero.MaxCPU, zero.MaxMemory, zero.MaxStorage)
	}
}

// TestDeployJobParams pins the placement the deploy Job runs with: the tenant's
// own <registry>/<tenant>-devops:<version> runtime image (image-baked config),
// in the platform namespace, under the cluster-admin deployer ServiceAccount.
func TestDeployJobParams(t *testing.T) {
	params := deployJobParams(
		EnvDeployConfig{
			Registry:               "ghcr.io/sophium",
			PlatformNamespace:      "erun-prod",
			DeployerServiceAccount: "erun-env-deployer",
		},
		EnvProvisionInput{Tenant: "acme", Environment: "prod", Version: "1.2.3"},
	)

	if params.Image != "ghcr.io/sophium/acme-devops:1.2.3" {
		t.Fatalf("image = %q, want ghcr.io/sophium/acme-devops:1.2.3", params.Image)
	}
	if params.Namespace != "erun-prod" {
		t.Fatalf("namespace = %q, want erun-prod", params.Namespace)
	}
	if params.ServiceAccount != "erun-env-deployer" {
		t.Fatalf("serviceAccount = %q, want erun-env-deployer", params.ServiceAccount)
	}
	if params.Tenant != "acme" || params.Environment != "prod" || params.Version != "1.2.3" {
		t.Fatalf("deploy coordinates = %+v", params)
	}
	if params.RuntimeImageOverride != "" {
		t.Fatalf("runtimeImageOverride = %q, want empty when the tenant publishes its own image (unbootstrapped)", params.RuntimeImageOverride)
	}
}

// TestDeployJobParamsBootstrap locks the fallback: a tenant confirmed to
// have never published its own image (Bootstrap, set by resolveBootstrapImage
// before the durable workflow runs) gets a Job that runs the canonical
// erun-devops image and threads --runtime-image at it, instead of a Job that
// can only ImagePullBackOff on an image nobody ever pushed.
func TestDeployJobParamsBootstrap(t *testing.T) {
	params := deployJobParams(
		EnvDeployConfig{
			Registry:               "ghcr.io/sophium",
			PlatformNamespace:      "erun-prod",
			DeployerServiceAccount: "erun-env-deployer",
		},
		EnvProvisionInput{Tenant: "acme", Environment: "prod", Version: "1.2.3", Bootstrap: true},
	)

	if params.Image != "ghcr.io/sophium/erun-devops:1.2.3" {
		t.Fatalf("image = %q, want the canonical ghcr.io/sophium/erun-devops:1.2.3", params.Image)
	}
	if params.RuntimeImageOverride != "ghcr.io/sophium/erun-devops:1.2.3" {
		t.Fatalf("runtimeImageOverride = %q, want it to match the canonical image", params.RuntimeImageOverride)
	}
}

// TestDeployJobParamsExposeTargetIP: the platform's ingress IP threads through
// to every deploy Job unchanged, so deployexec decides whether to chain
// expose. An empty config (the default) leaves it empty too, matching a
// platform with no ERUN_ENV_EXPOSE_TARGET_IP configured.
func TestDeployJobParamsExposeTargetIP(t *testing.T) {
	params := deployJobParams(
		EnvDeployConfig{
			Registry:               "ghcr.io/sophium",
			PlatformNamespace:      "erun-prod",
			DeployerServiceAccount: "erun-env-deployer",
			ExposeTargetIP:         "203.0.113.10",
		},
		EnvProvisionInput{Tenant: "acme", Environment: "prod", Version: "1.2.3"},
	)
	if params.ExposeTargetIP != "203.0.113.10" {
		t.Fatalf("exposeTargetIP = %q, want 203.0.113.10", params.ExposeTargetIP)
	}

	unset := deployJobParams(EnvDeployConfig{}, EnvProvisionInput{Tenant: "acme", Environment: "prod", Version: "1.2.3"})
	if unset.ExposeTargetIP != "" {
		t.Fatalf("exposeTargetIP = %q, want empty when unconfigured", unset.ExposeTargetIP)
	}
}

// TestDeployJobParamsExposePlatformCoordinates: the services zone and
// platform namespace the control plane already knows for its own purposes
// (DNS-01 cert issuance, Job placement) thread through so the chained expose
// step can resolve a hostname without a git checkout (#1086).
func TestDeployJobParamsExposePlatformCoordinates(t *testing.T) {
	params := deployJobParams(
		EnvDeployConfig{
			Registry:                "ghcr.io/sophium",
			PlatformNamespace:       "erun-prod",
			DeployerServiceAccount:  "erun-env-deployer",
			ExposeTargetIP:          "203.0.113.10",
			ExposeServicesZone:      "services.erunpaas.com",
			ExposePlatformNamespace: "frs-prod",
		},
		EnvProvisionInput{Tenant: "acme", Environment: "prod", Version: "1.2.3"},
	)
	if params.ExposeServicesZone != "services.erunpaas.com" {
		t.Fatalf("exposeServicesZone = %q, want services.erunpaas.com", params.ExposeServicesZone)
	}
	if params.ExposePlatformNamespace != "frs-prod" {
		t.Fatalf("exposePlatformNamespace = %q, want frs-prod", params.ExposePlatformNamespace)
	}

	unset := deployJobParams(EnvDeployConfig{}, EnvProvisionInput{Tenant: "acme", Environment: "prod", Version: "1.2.3"})
	if unset.ExposeServicesZone != "" || unset.ExposePlatformNamespace != "" {
		t.Fatalf("exposeServicesZone/exposePlatformNamespace = %q/%q, want both empty when unconfigured", unset.ExposeServicesZone, unset.ExposePlatformNamespace)
	}
}
