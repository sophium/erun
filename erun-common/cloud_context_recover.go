package eruncommon

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RecoverCloudContextParams carries the inputs the recovery flow
// cannot derive from the orphan record on its own. The CloudContext
// alias is required because describe-instances must run under a
// concrete cloud provider; the region is required for the EC2
// regional endpoint; admin-token recovery is optional and falls back
// to ~/.kube/config when left blank.
type RecoverCloudContextParams struct {
	KubernetesContext  string
	CloudProviderAlias string
	Region             string
	AdminToken         string
}

// RecoverCloudContextResult records what the recovery flow saved so
// the caller can surface the values back to the user (and so MCP
// callers can inspect them without re-reading the config).
type RecoverCloudContextResult struct {
	Saved      CloudContextConfig
	Source     string // "aws-describe-instances"
	TokenFrom  string // "kubeconfig" | "input" | "none"
}

type awsDescribeInstanceJSON struct {
	Reservations []struct {
		Instances []struct {
			InstanceID         string `json:"InstanceId"`
			State              struct {
				Name string `json:"Name"`
			} `json:"State"`
			PublicIPAddress    string `json:"PublicIpAddress,omitempty"`
			InstanceType       string `json:"InstanceType"`
			IamInstanceProfile struct {
				Arn string `json:"Arn"`
			} `json:"IamInstanceProfile,omitempty"`
			SecurityGroups []struct {
				GroupID string `json:"GroupId"`
			} `json:"SecurityGroups"`
			BlockDeviceMappings []struct {
				Ebs struct {
					VolumeID string `json:"VolumeId"`
				} `json:"Ebs"`
			} `json:"BlockDeviceMappings,omitempty"`
			LaunchTime string `json:"LaunchTime,omitempty"`
		} `json:"Instances"`
	} `json:"Reservations"`
}

type awsDescribeVolumeJSON struct {
	Volumes []struct {
		VolumeType string `json:"VolumeType"`
		Size       int    `json:"Size"`
	} `json:"Volumes"`
}

// RecoverCloudContextFromAWS rebuilds a CloudContextConfig for a
// missing context name by describing the matching EC2 instance,
// pulling the EBS volume's size + type, sourcing the bearer token
// from the supplied params or the local kubeconfig, and saving the
// result via the supplied store. Returns an error when the instance
// is missing, terminated, or ambiguous (multiple matches) — the
// caller surfaces those distinctly so the user can react.
//
// The function intentionally does not call IAM describe-instance-profile
// to populate InstanceProfileName / RoleName: those are only needed
// when re-init re-attaches a profile to the instance, and the
// already-running instance the recovery touches already has them in
// place. Leaving them blank in the restored config keeps the
// recovery scope tight while still producing a fully-usable
// CloudContextConfig for kubectl + start/stop.
func RecoverCloudContextFromAWS(ctx Context, store CloudContextStore, params RecoverCloudContextParams, deps CloudContextDependencies) (RecoverCloudContextResult, error) {
	params.KubernetesContext = strings.TrimSpace(params.KubernetesContext)
	params.CloudProviderAlias = strings.TrimSpace(params.CloudProviderAlias)
	params.Region = strings.TrimSpace(params.Region)
	if params.KubernetesContext == "" {
		return RecoverCloudContextResult{}, errors.New("kubernetes context is required")
	}
	if params.CloudProviderAlias == "" {
		return RecoverCloudContextResult{}, errors.New("cloud provider alias is required")
	}
	if params.Region == "" {
		return RecoverCloudContextResult{}, errors.New("region is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	provider, err := ResolveCloudProvider(store, params.CloudProviderAlias)
	if err != nil {
		return RecoverCloudContextResult{}, err
	}
	instance, err := describeCloudContextInstanceByName(ctx, deps, provider, params.Region, params.KubernetesContext)
	if err != nil {
		return RecoverCloudContextResult{}, err
	}
	disk, err := describeCloudContextVolume(ctx, deps, provider, params.Region, instance.volumeID)
	if err != nil {
		ctx.Trace("cloud-context recover: volume describe failed: " + err.Error())
		// Volume detail is best-effort. The caller can still save a
		// usable context with disk fields empty; treat the failure
		// as non-fatal trace + continue.
	}
	token, tokenFrom := resolveRecoverAdminToken(params)

	config := CloudContextConfig{
		Name:               params.KubernetesContext,
		Provider:           CloudProviderAWS,
		CloudProviderAlias: params.CloudProviderAlias,
		Region:             params.Region,
		KubernetesContext:  params.KubernetesContext,
		InstanceID:         instance.id,
		PublicIP:           instance.publicIP,
		InstanceType:       instance.instanceType,
		DiskType:           disk.volumeType,
		DiskSizeGB:         disk.size,
		AdminToken:         token,
		CreatedAt:          instance.launchTime,
		UpdatedAt:          deps.Now().UTC().Format(time.RFC3339),
	}
	if ctx.DryRun {
		ctx.TraceCommand("", "write-yaml", "erun-config:cloudcontext "+params.KubernetesContext)
		return RecoverCloudContextResult{Saved: config, Source: "aws-describe-instances", TokenFrom: tokenFrom}, nil
	}
	if err := saveCloudContextConfig(store, config); err != nil {
		return RecoverCloudContextResult{}, err
	}
	return RecoverCloudContextResult{Saved: config, Source: "aws-describe-instances", TokenFrom: tokenFrom}, nil
}

func resolveRecoverAdminToken(params RecoverCloudContextParams) (string, string) {
	if token := strings.TrimSpace(params.AdminToken); token != "" {
		return token, "input"
	}
	token, ok, err := LookupKubeContextBearerToken(params.KubernetesContext)
	if err != nil || !ok {
		return "", "none"
	}
	return token, "kubeconfig"
}

type recoveredInstance struct {
	id           string
	publicIP     string
	instanceType string
	securityGroup string
	instanceProfileARN string
	volumeID     string
	launchTime   string
}

type recoveredVolume struct {
	volumeType string
	size       int
}

func describeCloudContextInstanceByName(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, name string) (recoveredInstance, error) {
	out, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "describe-instances",
		"--filters",
		"Name=tag:Name,Values=" + name,
		"Name=instance-state-name,Values=pending,running,stopping,stopped",
		"--output", "json",
	})
	if err != nil {
		return recoveredInstance{}, err
	}
	if ctx.DryRun {
		return recoveredInstance{
			id:           "i-<recovered>",
			publicIP:     "203.0.113.10",
			instanceType: "c8gd.2xlarge",
			volumeID:     "vol-<recovered>",
			launchTime:   "2026-01-01T00:00:00Z",
		}, nil
	}
	parsed := awsDescribeInstanceJSON{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return recoveredInstance{}, fmt.Errorf("parse describe-instances: %w", err)
	}
	matches := flattenAWSDescribeInstances(parsed)
	switch len(matches) {
	case 0:
		return recoveredInstance{}, fmt.Errorf("no EC2 instance found with Name tag %q in region %s", name, region)
	case 1:
		return matches[0], nil
	default:
		return recoveredInstance{}, fmt.Errorf("multiple EC2 instances found with Name tag %q in region %s; resolve manually then re-run", name, region)
	}
}

func flattenAWSDescribeInstances(parsed awsDescribeInstanceJSON) []recoveredInstance {
	out := make([]recoveredInstance, 0)
	for _, reservation := range parsed.Reservations {
		for _, raw := range reservation.Instances {
			candidate := recoveredInstance{
				id:                 strings.TrimSpace(raw.InstanceID),
				publicIP:           strings.TrimSpace(raw.PublicIPAddress),
				instanceType:       strings.TrimSpace(raw.InstanceType),
				instanceProfileARN: strings.TrimSpace(raw.IamInstanceProfile.Arn),
				launchTime:         strings.TrimSpace(raw.LaunchTime),
			}
			if len(raw.SecurityGroups) > 0 {
				candidate.securityGroup = strings.TrimSpace(raw.SecurityGroups[0].GroupID)
			}
			if len(raw.BlockDeviceMappings) > 0 {
				candidate.volumeID = strings.TrimSpace(raw.BlockDeviceMappings[0].Ebs.VolumeID)
			}
			if candidate.id == "" {
				continue
			}
			out = append(out, candidate)
		}
	}
	return out
}

func describeCloudContextVolume(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, volumeID string) (recoveredVolume, error) {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" || strings.HasPrefix(volumeID, "vol-<") {
		return recoveredVolume{}, nil
	}
	out, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "describe-volumes",
		"--volume-ids", volumeID,
		"--output", "json",
	})
	if err != nil {
		return recoveredVolume{}, err
	}
	if ctx.DryRun {
		return recoveredVolume{volumeType: "gp3", size: 100}, nil
	}
	parsed := awsDescribeVolumeJSON{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return recoveredVolume{}, fmt.Errorf("parse describe-volumes: %w", err)
	}
	if len(parsed.Volumes) == 0 {
		return recoveredVolume{}, nil
	}
	return recoveredVolume{
		volumeType: strings.TrimSpace(parsed.Volumes[0].VolumeType),
		size:       parsed.Volumes[0].Size,
	}, nil
}
