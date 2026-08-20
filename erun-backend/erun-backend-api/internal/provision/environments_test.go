package provision

import (
	"context"
	"errors"
	"testing"
)

// stubImageChecker reports a fixed existence answer, or an error when set.
type stubImageChecker struct {
	exists   bool
	err      error
	calls    int
	gotImage string
}

func (c *stubImageChecker) Exists(_ context.Context, image string) (bool, error) {
	c.calls++
	c.gotImage = image
	return c.exists, c.err
}

// TestCheckImagePublished locks the synchronous precondition Start/StartDeploy
// run before enqueueing the durable workflow (#605): only a registry-confirmed
// absence blocks, matching RuntimeImageChecker's fail-open contract.
func TestCheckImagePublished(t *testing.T) {
	config := EnvDeployConfig{Registry: "ghcr.io/sophium"}
	input := EnvProvisionInput{Tenant: "acme", Version: "1.2.3"}

	t.Run("nil checker never blocks", func(t *testing.T) {
		p := &EnvProvisioner{config: config}
		if err := p.checkImagePublished(input); err != nil {
			t.Fatalf("checkImagePublished: %v, want nil", err)
		}
	})

	t.Run("confirmed missing blocks with the image named", func(t *testing.T) {
		checker := &stubImageChecker{exists: false}
		p := &EnvProvisioner{config: config, imageChecker: checker}
		err := p.checkImagePublished(input)
		var missing *MissingRuntimeImageError
		if !errors.As(err, &missing) {
			t.Fatalf("checkImagePublished = %v, want *MissingRuntimeImageError", err)
		}
		if missing.Image != "ghcr.io/sophium/acme-devops:1.2.3" {
			t.Fatalf("missing.Image = %q", missing.Image)
		}
		if checker.gotImage != "ghcr.io/sophium/acme-devops:1.2.3" {
			t.Fatalf("checker probed %q", checker.gotImage)
		}
	})

	t.Run("confirmed present does not block", func(t *testing.T) {
		p := &EnvProvisioner{config: config, imageChecker: &stubImageChecker{exists: true}}
		if err := p.checkImagePublished(input); err != nil {
			t.Fatalf("checkImagePublished: %v, want nil", err)
		}
	})

	t.Run("checker error does not block", func(t *testing.T) {
		p := &EnvProvisioner{config: config, imageChecker: &stubImageChecker{err: errors.New("network down")}}
		if err := p.checkImagePublished(input); err != nil {
			t.Fatalf("checkImagePublished: %v, want nil (fail open on checker error)", err)
		}
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
