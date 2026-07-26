package provision

import "testing"

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
