package eruncommon

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestInitCloudContextUsesDefaultsAndStoresKubeContext(t *testing.T) {
	store := &memoryCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{{
		Alias:     "rihards+123456789012@aws",
		Provider:  CloudProviderAWS,
		Profile:   "erun-sso",
		SSORegion: "eu-central-1",
		AccountID: "123456789012",
	}}}}
	var awsCommands []string
	var kubectlCommands []string
	status, err := InitCloudContext(Context{}, store, InitCloudContextParams{
		CloudProviderAlias: "rihards+123456789012@aws",
		DiskSizeGB:         AlternateCloudContextDiskSizeGB,
	}, CloudContextDependencies{
		Now:      func() time.Time { return time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC) },
		NewToken: func() string { return "test-token" },
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			awsCommands = append(awsCommands, strings.Join(args, " "))
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "ssm get-parameter"):
				return "ami-test\n", nil
			case strings.Contains(joined, "create-security-group"):
				return "sg-test\n", nil
			case strings.Contains(joined, "run-instances"):
				return "i-test\n", nil
			case strings.Contains(joined, "describe-instances"):
				return "198.51.100.10\n", nil
			default:
				return "", nil
			}
		},
		RunKubectl: func(_ Context, args []string) error {
			kubectlCommands = append(kubectlCommands, strings.Join(args, " "))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("InitCloudContext failed: %v", err)
	}
	if status.InstanceType != DefaultCloudContextInstanceType || status.DiskSizeGB != AlternateCloudContextDiskSizeGB || status.DiskType != DefaultCloudContextDiskType {
		t.Fatalf("unexpected defaults/options: %+v", status)
	}
	if status.Region != DefaultCloudContextRegion || status.Status != CloudContextStatusRunning || status.KubernetesContext == "" {
		t.Fatalf("unexpected stored context: %+v", status)
	}
	if status.Name != "erun-001-123456789012-eu-west-2" {
		t.Fatalf("unexpected generated context name: %+v", status)
	}
	if len(store.config.CloudContexts) != 1 || store.config.CloudContexts[0].InstanceID != "i-test" || store.config.CloudContexts[0].AdminToken != "test-token" {
		t.Fatalf("expected context to be saved with instance/token metadata, got %+v", store.config.CloudContexts)
	}
	if len(awsCommands) == 0 || len(kubectlCommands) != 3 {
		t.Fatalf("expected AWS and kubeconfig commands, got aws=%+v kubectl=%+v", awsCommands, kubectlCommands)
	}
}

func TestCloudContextPreflightDryRunTracesStartForStoppedContext(t *testing.T) {
	store := &memoryCloudStore{config: ERunConfig{
		CloudProviders: []CloudProviderConfig{{
			Alias:    "team-cloud",
			Provider: CloudProviderAWS,
			Profile:  "erun-sso",
		}},
		CloudContexts: []CloudContextConfig{{
			Name:               "team-context",
			Provider:           CloudProviderAWS,
			CloudProviderAlias: "team-cloud",
			Region:             DefaultCloudContextRegion,
			InstanceID:         "i-test",
			PublicIP:           "198.51.100.10",
			InstanceType:       DefaultCloudContextInstanceType,
			DiskType:           DefaultCloudContextDiskType,
			DiskSizeGB:         DefaultCloudContextDiskSizeGB,
			KubernetesContext:  "cluster-prod",
			AdminToken:         "test-token",
			Status:             CloudContextStatusStopped,
		}},
	}}
	trace := new(bytes.Buffer)
	ctx := Context{
		DryRun: true,
		Logger: NewLoggerWithWriters(2, trace, trace),
	}
	ctx.KubernetesContextPreflight = CloudContextPreflight(store, CloudContextDependencies{})

	if err := ctx.EnsureKubernetesContext("cluster-prod"); err != nil {
		t.Fatalf("EnsureKubernetesContext failed: %v", err)
	}

	output := trace.String()
	for _, want := range []string{
		"aws ec2 start-instances --instance-ids i-test --region eu-west-2 --profile erun-sso",
		"aws ec2 wait instance-running --instance-ids i-test --region eu-west-2 --profile erun-sso",
		"aws ec2 describe-instances --instance-ids i-test --query 'Reservations[0].Instances[0].PublicIpAddress' --output text --region eu-west-2 --profile erun-sso",
		"kubectl config set-cluster cluster-prod --server https://203.0.113.10:6443 --insecure-skip-tls-verify=true",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run trace to contain %q, got:\n%s", want, output)
		}
	}
	if store.config.CloudContexts[0].Status != CloudContextStatusStopped {
		t.Fatalf("dry-run should not persist cloud context start, got %+v", store.config.CloudContexts[0])
	}
}

func TestCloudContextPreflightSkipsRunningContext(t *testing.T) {
	store := &memoryCloudStore{config: ERunConfig{
		CloudProviders: []CloudProviderConfig{{
			Alias:    "team-cloud",
			Provider: CloudProviderAWS,
		}},
		CloudContexts: []CloudContextConfig{{
			Name:               "team-context",
			Provider:           CloudProviderAWS,
			CloudProviderAlias: "team-cloud",
			Region:             DefaultCloudContextRegion,
			InstanceID:         "i-test",
			KubernetesContext:  "cluster-prod",
			AdminToken:         "test-token",
			Status:             CloudContextStatusRunning,
		}},
	}}
	ctx := Context{}
	ctx.KubernetesContextPreflight = CloudContextPreflight(store, CloudContextDependencies{
		RunAWS: func(Context, CloudProviderConfig, string, []string) (string, error) {
			t.Fatal("did not expect AWS command for running cloud context")
			return "", nil
		},
		RunKubectl: func(Context, []string) error {
			t.Fatal("did not expect kubectl command for running cloud context")
			return nil
		},
	})

	if err := ctx.EnsureKubernetesContext("cluster-prod"); err != nil {
		t.Fatalf("EnsureKubernetesContext failed: %v", err)
	}
}

func TestInitCloudContextDryRunDoesNotSave(t *testing.T) {
	store := &memoryCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{{
		Alias:    "rihards+123456789012@aws",
		Provider: CloudProviderAWS,
		Profile:  "erun-sso",
	}}}}
	_, err := InitCloudContext(Context{DryRun: true}, store, InitCloudContextParams{
		CloudProviderAlias: "rihards+123456789012@aws",
		Region:             "eu-west-1",
	}, CloudContextDependencies{
		Now:      func() time.Time { return time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC) },
		NewToken: func() string { return "test-token" },
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "ssm get-parameter"):
				return "ami-test\n", nil
			case strings.Contains(joined, "create-security-group"):
				return "sg-test\n", nil
			case strings.Contains(joined, "run-instances"):
				return "i-test\n", nil
			case strings.Contains(joined, "describe-instances"):
				return "198.51.100.10\n", nil
			default:
				return "", nil
			}
		},
		RunKubectl: func(Context, []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("InitCloudContext dry-run failed: %v", err)
	}
	if len(store.config.CloudContexts) != 0 {
		t.Fatalf("dry-run should not save cloud contexts, got %+v", store.config.CloudContexts)
	}
}

func TestInitCloudContextGeneratedNameIncrementsForExistingContexts(t *testing.T) {
	store := &memoryCloudStore{config: ERunConfig{
		CloudProviders: []CloudProviderConfig{{
			Alias:     "rihards+123456789012@aws",
			Provider:  CloudProviderAWS,
			Profile:   "erun-sso",
			AccountID: "123456789012",
		}},
		CloudContexts: []CloudContextConfig{
			{Name: "erun-001-123456789012-eu-west-2"},
			{KubernetesContext: "erun-002-123456789012-eu-west-2"},
		},
	}}
	status, err := InitCloudContext(Context{DryRun: true}, store, InitCloudContextParams{
		CloudProviderAlias: "rihards+123456789012@aws",
	}, CloudContextDependencies{
		Now:        func() time.Time { return time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC) },
		NewToken:   func() string { return "test-token" },
		RunAWS:     func(Context, CloudProviderConfig, string, []string) (string, error) { return "", nil },
		RunKubectl: func(Context, []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("InitCloudContext failed: %v", err)
	}
	if status.Name != "erun-003-123456789012-eu-west-2" {
		t.Fatalf("expected incremented generated context name, got %+v", status)
	}
}
