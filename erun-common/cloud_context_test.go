package eruncommon

import (
	"bytes"
	"errors"
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
	if status.InstanceProfileName != "erun-001-123456789012-eu-west-2-host-stop" || status.InstanceRoleName != "erun-001-123456789012-eu-west-2-host-stop" {
		t.Fatalf("expected managed instance profile and role, got %+v", status)
	}
	if status.InstanceProfileARN != "arn:aws:iam::123456789012:instance-profile/erun-001-123456789012-eu-west-2-host-stop" {
		t.Fatalf("expected managed instance profile ARN, got %+v", status)
	}
	if len(store.config.CloudContexts) != 1 || store.config.CloudContexts[0].InstanceID != "i-test" || store.config.CloudContexts[0].AdminToken != "test-token" || store.config.CloudContexts[0].InstanceProfileName == "" || store.config.CloudContexts[0].InstanceProfileARN == "" || store.config.CloudContexts[0].InstanceRoleName == "" {
		t.Fatalf("expected context to be saved with instance/token metadata, got %+v", store.config.CloudContexts)
	}
	if len(awsCommands) == 0 || len(kubectlCommands) != 3 {
		t.Fatalf("expected AWS and kubeconfig commands, got aws=%+v kubectl=%+v", awsCommands, kubectlCommands)
	}
	for _, want := range []string{
		"iam put-role-policy --role-name erun-001-123456789012-eu-west-2-host-stop --policy-name erun-self-stop",
		"iam add-role-to-instance-profile --instance-profile-name erun-001-123456789012-eu-west-2-host-stop --role-name erun-001-123456789012-eu-west-2-host-stop",
		"ec2 run-instances",
		"--iam-instance-profile Arn=arn:aws:iam::123456789012:instance-profile/erun-001-123456789012-eu-west-2-host-stop",
		"--metadata-options HttpEndpoint=enabled,HttpTokens=required,HttpPutResponseHopLimit=2",
	} {
		if !strings.Contains(strings.Join(awsCommands, "\n"), want) {
			t.Fatalf("expected AWS commands to contain %q, got %+v", want, awsCommands)
		}
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
		"aws iam create-role --role-name erun-team-context-host-stop",
		"aws ec2 associate-iam-instance-profile --instance-id i-test --iam-instance-profile Name=erun-team-context-host-stop --region eu-west-2 --profile erun-sso",
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

func TestEnsureCloudContextHostStopProfileAssociationReplacesExistingProfile(t *testing.T) {
	store := &memoryCloudStore{config: ERunConfig{
		CloudProviders: []CloudProviderConfig{{
			Alias:     "team-cloud",
			Provider:  CloudProviderAWS,
			Profile:   "erun-sso",
			AccountID: "123456789012",
		}},
		CloudContexts: []CloudContextConfig{{
			Name:               "team-context",
			Provider:           CloudProviderAWS,
			CloudProviderAlias: "team-cloud",
			Region:             DefaultCloudContextRegion,
			InstanceID:         "i-test",
			InstanceType:       DefaultCloudContextInstanceType,
			DiskType:           DefaultCloudContextDiskType,
			DiskSizeGB:         DefaultCloudContextDiskSizeGB,
			KubernetesContext:  "cluster-prod",
			Status:             CloudContextStatusStopped,
		}},
	}}
	var awsCommands []string
	err := ensureCloudContextHostStopProfileAssociation(Context{}, store, CloudContextParams{Name: "team-context"}, CloudContextDependencies{
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			awsCommands = append(awsCommands, joined)
			switch {
			case strings.Contains(joined, "InstanceProfile.Roles[0].RoleName"):
				return "erun-team-context-host-stop\n", nil
			case strings.Contains(joined, "get-instance-profile"):
				return "arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop\n", nil
			case strings.Contains(joined, "describe-iam-instance-profile-associations") && strings.Contains(joined, "Name=state,Values=associated"):
				return "iip-assoc-test\n", nil
			default:
				return "", nil
			}
		},
	})
	if err != nil {
		t.Fatalf("ensureCloudContextHostStopProfileAssociation failed: %v", err)
	}
	joined := strings.Join(awsCommands, "\n")
	if !strings.Contains(joined, "ec2 replace-iam-instance-profile-association --association-id iip-assoc-test --iam-instance-profile Arn=arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop") {
		t.Fatalf("expected existing association to be replaced, got %+v", awsCommands)
	}
	if strings.Contains(joined, "ec2 associate-iam-instance-profile") {
		t.Fatalf("expected no associate call when association already exists, got %+v", awsCommands)
	}
	if store.config.CloudContexts[0].InstanceProfileARN != "arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop" {
		t.Fatalf("expected saved profile ARN, got %+v", store.config.CloudContexts[0])
	}
}

func TestEnsureCloudContextHostStopProfileAssociationSkipsMatchingProfile(t *testing.T) {
	store := &memoryCloudStore{config: ERunConfig{
		CloudProviders: []CloudProviderConfig{{
			Alias:     "team-cloud",
			Provider:  CloudProviderAWS,
			Profile:   "erun-sso",
			AccountID: "123456789012",
		}},
		CloudContexts: []CloudContextConfig{{
			Name:               "team-context",
			Provider:           CloudProviderAWS,
			CloudProviderAlias: "team-cloud",
			Region:             DefaultCloudContextRegion,
			InstanceID:         "i-test",
			InstanceType:       DefaultCloudContextInstanceType,
			DiskType:           DefaultCloudContextDiskType,
			DiskSizeGB:         DefaultCloudContextDiskSizeGB,
			KubernetesContext:  "cluster-prod",
			Status:             CloudContextStatusStopped,
		}},
	}}
	var awsCommands []string
	err := ensureCloudContextHostStopProfileAssociation(Context{}, store, CloudContextParams{Name: "team-context"}, CloudContextDependencies{
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			awsCommands = append(awsCommands, joined)
			switch {
			case strings.Contains(joined, "InstanceProfile.Roles[0].RoleName"):
				return "erun-team-context-host-stop\n", nil
			case strings.Contains(joined, "get-instance-profile"):
				return "arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop\n", nil
			case strings.Contains(joined, "IamInstanceProfileAssociations[0].AssociationId"):
				return "iip-assoc-test\n", nil
			case strings.Contains(joined, "IamInstanceProfileAssociations[0].IamInstanceProfile.Arn"):
				return "arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop\n", nil
			default:
				return "", nil
			}
		},
	})
	if err != nil {
		t.Fatalf("ensureCloudContextHostStopProfileAssociation failed: %v", err)
	}
	joined := strings.Join(awsCommands, "\n")
	if strings.Contains(joined, "replace-iam-instance-profile-association") || strings.Contains(joined, "associate-iam-instance-profile") {
		t.Fatalf("expected no association mutation when profile already matches, got %+v", awsCommands)
	}
}

func TestEnsureCloudContextInstanceProfileSkipsRoleAddWhenRoleAlreadyAttached(t *testing.T) {
	var awsCommands []string
	profile, err := ensureCloudContextInstanceProfile(Context{}, CloudContextDependencies{
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			awsCommands = append(awsCommands, joined)
			switch {
			case strings.Contains(joined, "InstanceProfile.Roles[0].RoleName"):
				return "erun-team-context-host-stop\n", nil
			case strings.Contains(joined, "InstanceProfile.Arn"):
				return "arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop\n", nil
			default:
				return "", nil
			}
		},
	}, CloudProviderConfig{AccountID: "123456789012"}, DefaultCloudContextRegion, "team-context")
	if err != nil {
		t.Fatalf("ensureCloudContextInstanceProfile failed: %v", err)
	}
	if profile.RoleName != "erun-team-context-host-stop" || profile.ARN != "arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop" {
		t.Fatalf("unexpected instance profile: %+v", profile)
	}
	joined := strings.Join(awsCommands, "\n")
	if !strings.Contains(joined, "InstanceProfile.Roles[0].RoleName") {
		t.Fatalf("expected existing profile role check, got %+v", awsCommands)
	}
	if strings.Contains(joined, "add-role-to-instance-profile") {
		t.Fatalf("expected no add-role call when role is already attached, got %+v", awsCommands)
	}
}

func TestEnsureCloudContextInstanceProfileReportsDifferentAttachedRole(t *testing.T) {
	_, err := ensureCloudContextInstanceProfile(Context{}, CloudContextDependencies{
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "InstanceProfile.Roles[0].RoleName"):
				return "other-role\n", nil
			case strings.Contains(joined, "InstanceProfile.Arn"):
				return "arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop\n", nil
			default:
				return "", nil
			}
		},
	}, CloudProviderConfig{AccountID: "123456789012"}, DefaultCloudContextRegion, "team-context")
	if err == nil || !strings.Contains(err.Error(), `instance profile "erun-team-context-host-stop" already contains role "other-role"; expected "erun-team-context-host-stop"`) {
		t.Fatalf("expected different attached role error, got %v", err)
	}
}

func TestEnsureCloudContextInstanceProfileAssociationSkipsPendingAssociation(t *testing.T) {
	var awsCommands []string
	err := ensureCloudContextInstanceProfileAssociation(Context{}, CloudContextDependencies{
		Sleep: func(time.Duration) {},
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			awsCommands = append(awsCommands, joined)
			switch {
			case strings.Contains(joined, "Name=state,Values=associated"):
				return "None\n", nil
			case strings.Contains(joined, "Name=state,Values=associating,disassociating"):
				return "iip-assoc-pending\n", nil
			default:
				return "", nil
			}
		},
	}, CloudProviderConfig{}, DefaultCloudContextRegion, "i-test", "Arn=arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop")
	if err != nil {
		t.Fatalf("ensureCloudContextInstanceProfileAssociation failed: %v", err)
	}
	joined := strings.Join(awsCommands, "\n")
	if !strings.Contains(joined, "Name=state,Values=associated") || !strings.Contains(joined, "Name=state,Values=associating,disassociating") {
		t.Fatalf("expected active and pending association checks, got %+v", awsCommands)
	}
	if strings.Contains(joined, "ec2 replace-iam-instance-profile-association") {
		t.Fatalf("expected no replace call while association is pending, got %+v", awsCommands)
	}
	if strings.Contains(joined, "ec2 associate-iam-instance-profile") {
		t.Fatalf("expected no associate call while association is pending, got %+v", awsCommands)
	}
}

func TestEnsureCloudContextInstanceProfileAssociationRecoversExistingAssociationRace(t *testing.T) {
	var awsCommands []string
	err := ensureCloudContextInstanceProfileAssociation(Context{}, CloudContextDependencies{
		Sleep: func(time.Duration) {},
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			awsCommands = append(awsCommands, joined)
			switch {
			case strings.Contains(joined, "associate-iam-instance-profile"):
				return "", errors.New("An error occurred (IncorrectState) when calling the AssociateIamInstanceProfile operation: There is an existing association for instance i-test")
			case strings.Contains(joined, "Name=state,Values=associated"):
				return "None\n", nil
			case strings.Contains(joined, "Name=state,Values=associating,disassociating"):
				return "None\n", nil
			case strings.Contains(joined, "describe-iam-instance-profile-associations") && strings.Contains(joined, "Name=instance-id,Values=i-test"):
				return "iip-assoc-race\n", nil
			default:
				return "", nil
			}
		},
	}, CloudProviderConfig{}, DefaultCloudContextRegion, "i-test", "Arn=arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop")
	if err != nil {
		t.Fatalf("ensureCloudContextInstanceProfileAssociation failed: %v", err)
	}
	joined := strings.Join(awsCommands, "\n")
	if !strings.Contains(joined, "ec2 associate-iam-instance-profile --instance-id i-test --iam-instance-profile Arn=arn:aws:iam::123456789012:instance-profile/erun-team-context-host-stop") {
		t.Fatalf("expected associate attempt, got %+v", awsCommands)
	}
	if strings.Contains(joined, "ec2 replace-iam-instance-profile-association") {
		t.Fatalf("expected no replace retry after associate race, got %+v", awsCommands)
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
