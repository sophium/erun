package eruncommon

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultCloudContextInstanceType   = "c8gd.2xlarge"
	AlternateCloudContextInstanceType = "t4g.xlarge"
	DefaultCloudContextDiskType       = "gp3"
	DefaultCloudContextDiskSizeGB     = 100
	AlternateCloudContextDiskSizeGB   = 200
	DefaultCloudContextRegion         = "eu-west-2"
	AlternateCloudContextRegion       = "eu-west-1"

	CloudContextStatusPending = "pending"
	CloudContextStatusRunning = "running"
	CloudContextStatusStopped = "stopped"
	CloudContextStatusUnknown = "unknown"
)

type CloudContextStore interface {
	CloudStore
}

type CloudContextConfig struct {
	Name                string `json:"name" yaml:"name"`
	Provider            string `json:"provider" yaml:"provider"`
	CloudProviderAlias  string `json:"cloudProviderAlias" yaml:"cloudprovideralias"`
	Region              string `json:"region" yaml:"region"`
	InstanceID          string `json:"instanceId,omitempty" yaml:"instanceid,omitempty"`
	PublicIP            string `json:"publicIp,omitempty" yaml:"publicip,omitempty"`
	InstanceType        string `json:"instanceType" yaml:"instancetype"`
	DiskType            string `json:"diskType" yaml:"disktype"`
	DiskSizeGB          int    `json:"diskSizeGb" yaml:"disksizegb"`
	KubernetesContext   string `json:"kubernetesContext" yaml:"kubernetescontext"`
	SecurityGroupID     string `json:"securityGroupId,omitempty" yaml:"securitygroupid,omitempty"`
	InstanceProfileName string `json:"instanceProfileName,omitempty" yaml:"instanceprofilename,omitempty"`
	InstanceProfileARN  string `json:"instanceProfileArn,omitempty" yaml:"instanceprofilearn,omitempty"`
	InstanceRoleName    string `json:"instanceRoleName,omitempty" yaml:"instancerolename,omitempty"`
	AdminToken          string `json:"-" yaml:"admintoken,omitempty"`
	Status              string `json:"status" yaml:"status"`
	CreatedAt           string `json:"createdAt,omitempty" yaml:"createdat,omitempty"`
	UpdatedAt           string `json:"updatedAt,omitempty" yaml:"updatedat,omitempty"`
}

type CloudContextStatus struct {
	CloudContextConfig `json:",inline" yaml:",inline"`
	Message            string `json:"message,omitempty" yaml:"message,omitempty"`
}

type InitCloudContextParams struct {
	Name               string
	CloudProviderAlias string
	Region             string
	InstanceType       string
	DiskType           string
	DiskSizeGB         int
	SubnetID           string
	SecurityGroupID    string
	KeyName            string
}

type CloudContextParams struct {
	Name string
}

type CloudContextDependencies struct {
	RunAWS     func(Context, CloudProviderConfig, string, []string) (string, error)
	RunKubectl func(Context, []string) error
	Now        func() time.Time
	Sleep      func(time.Duration)
	NewToken   func() string
}

func CloudContextInstanceTypes() []string {
	return []string{DefaultCloudContextInstanceType, AlternateCloudContextInstanceType}
}

func CloudContextDiskSizesGB() []int {
	return []int{DefaultCloudContextDiskSizeGB, AlternateCloudContextDiskSizeGB}
}

func CloudContextRegions() []string {
	return []string{DefaultCloudContextRegion, AlternateCloudContextRegion}
}

func NormalizeCloudContextConfig(config CloudContextConfig) CloudContextConfig {
	config.Name = strings.TrimSpace(config.Name)
	config.Provider = strings.ToLower(strings.TrimSpace(config.Provider))
	config.CloudProviderAlias = strings.TrimSpace(config.CloudProviderAlias)
	config.Region = strings.TrimSpace(config.Region)
	config.InstanceID = strings.TrimSpace(config.InstanceID)
	config.PublicIP = strings.TrimSpace(config.PublicIP)
	config.InstanceType = strings.TrimSpace(config.InstanceType)
	config.DiskType = strings.TrimSpace(config.DiskType)
	config.KubernetesContext = strings.TrimSpace(config.KubernetesContext)
	if config.Name == "" {
		config.Name = config.KubernetesContext
	}
	config.SecurityGroupID = strings.TrimSpace(config.SecurityGroupID)
	config.InstanceProfileName = strings.TrimSpace(config.InstanceProfileName)
	config.InstanceProfileARN = strings.TrimSpace(config.InstanceProfileARN)
	config.InstanceRoleName = strings.TrimSpace(config.InstanceRoleName)
	config.AdminToken = strings.TrimSpace(config.AdminToken)
	config.Status = strings.TrimSpace(config.Status)
	config.CreatedAt = strings.TrimSpace(config.CreatedAt)
	config.UpdatedAt = strings.TrimSpace(config.UpdatedAt)
	if config.InstanceType == "" {
		config.InstanceType = DefaultCloudContextInstanceType
	}
	if config.DiskType == "" {
		config.DiskType = DefaultCloudContextDiskType
	}
	if config.DiskSizeGB == 0 {
		config.DiskSizeGB = DefaultCloudContextDiskSizeGB
	}
	if config.KubernetesContext == "" {
		config.KubernetesContext = config.Name
	}
	if config.Status == "" {
		config.Status = CloudContextStatusUnknown
	}
	return config
}

func ListCloudContexts(store CloudReadStore) ([]CloudContextConfig, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	config, _, err := store.LoadERunConfig()
	if err == ErrNotInitialized {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return normalizedCloudContexts(config.CloudContexts), nil
}

func ListCloudContextStatuses(store CloudReadStore) ([]CloudContextStatus, error) {
	contexts, err := ListCloudContexts(store)
	if err != nil {
		return nil, err
	}
	statuses := make([]CloudContextStatus, 0, len(contexts))
	for _, context := range contexts {
		statuses = append(statuses, CloudContextStatus{CloudContextConfig: context})
	}
	return statuses, nil
}

func InitCloudContext(ctx Context, store CloudContextStore, params InitCloudContextParams, deps CloudContextDependencies) (CloudContextStatus, error) {
	if store == nil {
		return CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	provider, err := ResolveCloudProvider(store, params.CloudProviderAlias)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if provider.Provider != CloudProviderAWS {
		return CloudContextStatus{}, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
	existingContexts, err := ListCloudContexts(store)
	if err != nil {
		return CloudContextStatus{}, err
	}
	config, err := resolveInitCloudContextConfig(provider, params, deps.Now(), existingContexts)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if existing, ok, err := findCloudContext(store, config.Name); err != nil {
		return CloudContextStatus{}, err
	} else if ok && existing.InstanceID != "" {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q already exists", config.Name)
	}

	ami, err := deps.RunAWS(ctx, provider, config.Region, []string{
		"ssm", "get-parameter",
		"--name", "/aws/service/canonical/ubuntu/server/24.04/stable/current/arm64/hvm/ebs-gp3/ami-id",
		"--query", "Parameter.Value",
		"--output", "text",
	})
	if err != nil {
		return CloudContextStatus{}, err
	}
	ami = strings.TrimSpace(ami)
	if ami == "" {
		ami = "ami-<latest-ubuntu-arm64>"
	}

	securityGroupID := strings.TrimSpace(params.SecurityGroupID)
	if securityGroupID == "" {
		securityGroupID, err = createCloudContextSecurityGroup(ctx, deps, provider, config.Region, config.Name)
		if err != nil {
			return CloudContextStatus{}, err
		}
	}
	config.SecurityGroupID = securityGroupID
	instanceProfile, err := ensureCloudContextInstanceProfile(ctx, deps, provider, config.Region, config.Name)
	if err != nil {
		return CloudContextStatus{}, err
	}
	config.InstanceProfileName = instanceProfile.Name
	config.InstanceProfileARN = instanceProfile.ARN
	config.InstanceRoleName = instanceProfile.RoleName

	config.AdminToken = deps.NewToken()
	userDataPath, cleanup, err := cloudContextUserDataFile(ctx, config.AdminToken)
	if err != nil {
		return CloudContextStatus{}, err
	}
	defer cleanup()

	runArgs := []string{
		"ec2", "run-instances",
		"--image-id", ami,
		"--instance-type", config.InstanceType,
		"--count", "1",
		"--block-device-mappings", fmt.Sprintf("DeviceName=/dev/sda1,Ebs={VolumeSize=%d,VolumeType=%s,DeleteOnTermination=true}", config.DiskSizeGB, config.DiskType),
		"--user-data", "file://" + userDataPath,
		"--metadata-options", "HttpEndpoint=enabled,HttpTokens=required,HttpPutResponseHopLimit=2",
		"--tag-specifications", fmt.Sprintf("ResourceType=instance,Tags=[{Key=Name,Value=%s},{Key=erun:context,Value=%s}]", config.Name, config.Name),
		"--query", "Instances[0].InstanceId",
		"--output", "text",
	}
	if config.SecurityGroupID != "" {
		runArgs = append(runArgs, "--security-group-ids", config.SecurityGroupID)
	}
	if config.InstanceProfileARN != "" {
		runArgs = append(runArgs, "--iam-instance-profile", "Arn="+config.InstanceProfileARN)
	} else if config.InstanceProfileName != "" {
		runArgs = append(runArgs, "--iam-instance-profile", "Name="+config.InstanceProfileName)
	}
	if subnetID := strings.TrimSpace(params.SubnetID); subnetID != "" {
		runArgs = append(runArgs, "--subnet-id", subnetID)
	}
	if keyName := strings.TrimSpace(params.KeyName); keyName != "" {
		runArgs = append(runArgs, "--key-name", keyName)
	}
	instanceID, err := deps.RunAWS(ctx, provider, config.Region, runArgs)
	if err != nil {
		return CloudContextStatus{}, err
	}
	config.InstanceID = strings.TrimSpace(instanceID)
	if config.InstanceID == "" {
		config.InstanceID = "i-<new-instance>"
	}
	config.Status = CloudContextStatusPending

	if _, err := deps.RunAWS(ctx, provider, config.Region, []string{"ec2", "wait", "instance-running", "--instance-ids", config.InstanceID}); err != nil {
		return CloudContextStatus{}, err
	}
	publicIP, err := describeCloudContextPublicIP(ctx, deps, provider, config.Region, config.InstanceID)
	if err != nil {
		return CloudContextStatus{}, err
	}
	config.PublicIP = publicIP
	if err := configureCloudKubeContext(ctx, deps, config); err != nil {
		return CloudContextStatus{}, err
	}
	config.Status = CloudContextStatusRunning
	config.UpdatedAt = deps.Now().UTC().Format(time.RFC3339)
	if ctx.DryRun {
		return CloudContextStatus{CloudContextConfig: NormalizeCloudContextConfig(config)}, nil
	}
	if err := saveCloudContextConfig(store, config); err != nil {
		return CloudContextStatus{}, err
	}
	return CloudContextStatus{CloudContextConfig: NormalizeCloudContextConfig(config)}, nil
}

func StopCloudContext(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) (CloudContextStatus, error) {
	return changeCloudContextPowerState(ctx, store, params, deps, "stop-instances", CloudContextStatusStopped)
}

func StartCloudContext(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) (CloudContextStatus, error) {
	if err := ensureCloudContextHostStopProfileAssociation(ctx, store, params, deps); err != nil {
		ctx.Trace("skipping cloud context host-stop profile association: " + err.Error())
	}
	status, err := changeCloudContextPowerState(ctx, store, params, deps, "start-instances", CloudContextStatusRunning)
	if err != nil {
		return CloudContextStatus{}, err
	}
	deps = normalizeCloudContextDependencies(deps)
	provider, err := ResolveCloudProvider(store, status.CloudProviderAlias)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if _, err := deps.RunAWS(ctx, provider, status.Region, []string{"ec2", "wait", "instance-running", "--instance-ids", status.InstanceID}); err != nil {
		return CloudContextStatus{}, err
	}
	publicIP, err := describeCloudContextPublicIP(ctx, deps, provider, status.Region, status.InstanceID)
	if err != nil {
		return CloudContextStatus{}, err
	}
	status.PublicIP = publicIP
	if err := configureCloudKubeContext(ctx, deps, status.CloudContextConfig); err != nil {
		return CloudContextStatus{}, err
	}
	status.UpdatedAt = deps.Now().UTC().Format(time.RFC3339)
	if ctx.DryRun {
		return status, nil
	}
	if err := saveCloudContextConfig(store, status.CloudContextConfig); err != nil {
		return CloudContextStatus{}, err
	}
	return status, nil
}

func ensureCloudContextHostStopProfileAssociation(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) error {
	if store == nil {
		return fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	config, ok, err := findCloudContext(store, params.Name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(params.Name))
	}
	if strings.TrimSpace(config.InstanceID) == "" {
		return nil
	}
	provider, err := ResolveCloudProvider(store, config.CloudProviderAlias)
	if err != nil {
		return err
	}
	instanceProfile, err := ensureCloudContextInstanceProfile(ctx, deps, provider, config.Region, config.Name)
	if err != nil {
		return err
	}
	profileRef := "Name=" + instanceProfile.Name
	if instanceProfile.ARN != "" {
		profileRef = "Arn=" + instanceProfile.ARN
	}
	if err := ensureCloudContextInstanceProfileAssociation(ctx, deps, provider, config.Region, config.InstanceID, profileRef); err != nil {
		return err
	}

	config.InstanceProfileName = instanceProfile.Name
	config.InstanceProfileARN = instanceProfile.ARN
	config.InstanceRoleName = instanceProfile.RoleName
	if ctx.DryRun {
		return nil
	}
	return saveCloudContextConfig(store, config)
}

func ensureCloudContextInstanceProfileAssociation(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID, profileRef string) error {
	deps = normalizeCloudContextDependencies(deps)
	associationID, err := activeCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, instanceID)
	if err != nil {
		return err
	}
	if associationID != "" {
		associationARN, err := activeCloudContextInstanceProfileAssociationARN(ctx, deps, provider, region, instanceID)
		if err != nil {
			return err
		}
		if profileRefMatchesAssociation(profileRef, associationARN) {
			return nil
		}
		return replaceCloudContextInstanceProfileAssociation(ctx, deps, provider, region, instanceID, associationID, profileRef)
	}

	pendingAssociationID, err := pendingCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, instanceID)
	if err != nil {
		return err
	}
	if pendingAssociationID != "" {
		return nil
	}

	if _, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "associate-iam-instance-profile",
		"--instance-id", instanceID,
		"--iam-instance-profile", profileRef,
	}); err != nil {
		if !isAlreadyAssociatedAWSError(err) && !isExistingInstanceProfileAssociationError(err) {
			return err
		}
	}
	return nil
}

func activeCloudContextInstanceProfileAssociationID(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID string) (string, error) {
	return describeCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, []string{
		"Name=instance-id,Values=" + instanceID,
		"Name=state,Values=associated",
	})
}

func activeCloudContextInstanceProfileAssociationARN(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID string) (string, error) {
	return describeCloudContextInstanceProfileAssociationARN(ctx, deps, provider, region, []string{
		"Name=instance-id,Values=" + instanceID,
		"Name=state,Values=associated",
	})
}

func pendingCloudContextInstanceProfileAssociationID(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID string) (string, error) {
	return describeCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, []string{
		"Name=instance-id,Values=" + instanceID,
		"Name=state,Values=associating,disassociating",
	})
}

func describeCloudContextInstanceProfileAssociationID(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region string, filters []string) (string, error) {
	args := []string{
		"ec2", "describe-iam-instance-profile-associations",
		"--filters",
	}
	args = append(args, filters...)
	args = append(args,
		"--query", "IamInstanceProfileAssociations[0].AssociationId",
		"--output", "text",
	)
	associationID, err := deps.RunAWS(ctx, provider, region, args)
	if err != nil {
		return "", err
	}
	associationID = strings.TrimSpace(associationID)
	if associationID == "" || strings.EqualFold(associationID, "none") {
		return "", nil
	}
	return associationID, nil
}

func describeCloudContextInstanceProfileAssociationARN(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region string, filters []string) (string, error) {
	args := []string{
		"ec2", "describe-iam-instance-profile-associations",
		"--filters",
	}
	args = append(args, filters...)
	args = append(args,
		"--query", "IamInstanceProfileAssociations[0].IamInstanceProfile.Arn",
		"--output", "text",
	)
	arn, err := deps.RunAWS(ctx, provider, region, args)
	if err != nil {
		return "", err
	}
	arn = strings.TrimSpace(arn)
	if arn == "" || strings.EqualFold(arn, "none") {
		return "", nil
	}
	return arn, nil
}

func profileRefMatchesAssociation(profileRef, associationARN string) bool {
	profileRef = strings.TrimSpace(profileRef)
	associationARN = strings.TrimSpace(associationARN)
	return associationARN != "" && strings.TrimPrefix(profileRef, "Arn=") == associationARN
}

func replaceCloudContextInstanceProfileAssociation(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID, associationID, profileRef string) error {
	associationID = strings.TrimSpace(associationID)
	if associationID == "" {
		return nil
	}
	_, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "replace-iam-instance-profile-association",
		"--association-id", associationID,
		"--iam-instance-profile", profileRef,
	})
	return err
}

func CloudContextPreflight(store CloudContextStore, deps CloudContextDependencies) KubernetesContextPreflightFunc {
	var mu sync.Mutex
	started := make(map[string]struct{})
	return func(ctx Context, kubernetesContext string) error {
		kubernetesContext = strings.TrimSpace(kubernetesContext)
		if kubernetesContext == "" || store == nil {
			return nil
		}

		mu.Lock()
		if _, ok := started[kubernetesContext]; ok {
			mu.Unlock()
			return nil
		}
		mu.Unlock()

		status, ok, err := findCloudContextForKubernetesContext(store, kubernetesContext)
		if err != nil || !ok {
			return err
		}
		if strings.TrimSpace(status.Status) == CloudContextStatusRunning {
			return nil
		}

		if _, err := StartCloudContext(ctx, store, CloudContextParams{Name: status.Name}, deps); err != nil {
			return err
		}

		mu.Lock()
		started[kubernetesContext] = struct{}{}
		if name := strings.TrimSpace(status.Name); name != "" {
			started[name] = struct{}{}
		}
		mu.Unlock()
		return nil
	}
}

func findCloudContextForKubernetesContext(store CloudReadStore, kubernetesContext string) (CloudContextStatus, bool, error) {
	kubernetesContext = strings.TrimSpace(kubernetesContext)
	if kubernetesContext == "" {
		return CloudContextStatus{}, false, nil
	}
	contexts, err := ListCloudContexts(store)
	if err != nil {
		return CloudContextStatus{}, false, err
	}
	for _, context := range contexts {
		context = NormalizeCloudContextConfig(context)
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			return CloudContextStatus{CloudContextConfig: context}, true, nil
		}
	}
	return CloudContextStatus{}, false, nil
}

func changeCloudContextPowerState(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies, awsAction, status string) (CloudContextStatus, error) {
	if store == nil {
		return CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	config, ok, err := findCloudContext(store, params.Name)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if !ok {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(params.Name))
	}
	if config.InstanceID == "" {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q has no instance ID", config.Name)
	}
	provider, err := ResolveCloudProvider(store, config.CloudProviderAlias)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if _, err := deps.RunAWS(ctx, provider, config.Region, []string{"ec2", awsAction, "--instance-ids", config.InstanceID}); err != nil {
		return CloudContextStatus{}, err
	}
	config.Status = status
	config.UpdatedAt = deps.Now().UTC().Format(time.RFC3339)
	if ctx.DryRun {
		return CloudContextStatus{CloudContextConfig: NormalizeCloudContextConfig(config)}, nil
	}
	if err := saveCloudContextConfig(store, config); err != nil {
		return CloudContextStatus{}, err
	}
	return CloudContextStatus{CloudContextConfig: NormalizeCloudContextConfig(config)}, nil
}

func resolveInitCloudContextConfig(provider CloudProviderConfig, params InitCloudContextParams, now time.Time, existingContexts []CloudContextConfig) (CloudContextConfig, error) {
	config := CloudContextConfig{
		Name:               strings.TrimSpace(params.Name),
		Provider:           CloudProviderAWS,
		CloudProviderAlias: provider.Alias,
		Region:             strings.TrimSpace(params.Region),
		InstanceType:       strings.TrimSpace(params.InstanceType),
		DiskType:           strings.TrimSpace(params.DiskType),
		DiskSizeGB:         params.DiskSizeGB,
		Status:             CloudContextStatusPending,
		CreatedAt:          now.UTC().Format(time.RFC3339),
		UpdatedAt:          now.UTC().Format(time.RFC3339),
	}
	if config.Region == "" {
		config.Region = DefaultCloudContextRegion
	}
	if config.Name == "" {
		config.Name = generatedCloudContextName(provider, config.Region, existingContexts)
	}
	config = NormalizeCloudContextConfig(config)
	if config.CloudProviderAlias == "" {
		return CloudContextConfig{}, fmt.Errorf("cloud provider alias is required")
	}
	if config.Region == "" {
		return CloudContextConfig{}, fmt.Errorf("cloud context region is required")
	}
	if !validCloudContextRegion(config.Region) {
		return CloudContextConfig{}, fmt.Errorf("unsupported cloud context region %q", config.Region)
	}
	if !validCloudContextInstanceType(config.InstanceType) {
		return CloudContextConfig{}, fmt.Errorf("unsupported cloud context instance type %q", config.InstanceType)
	}
	if !validCloudContextDiskSize(config.DiskSizeGB) {
		return CloudContextConfig{}, fmt.Errorf("unsupported cloud context disk size %d", config.DiskSizeGB)
	}
	if config.DiskType != DefaultCloudContextDiskType {
		return CloudContextConfig{}, fmt.Errorf("unsupported cloud context disk type %q", config.DiskType)
	}
	return config, nil
}

func createCloudContextSecurityGroup(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, name string) (string, error) {
	groupName := name + "-k3s"
	groupID, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "create-security-group",
		"--group-name", groupName,
		"--description", "ERun managed k3s API access for " + name,
		"--query", "GroupId",
		"--output", "text",
	})
	if err != nil {
		return "", err
	}
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		groupID = "sg-<" + name + ">"
	}
	_, err = deps.RunAWS(ctx, provider, region, []string{
		"ec2", "authorize-security-group-ingress",
		"--group-id", groupID,
		"--protocol", "tcp",
		"--port", "6443",
		"--cidr", "0.0.0.0/0",
	})
	return groupID, err
}

type cloudContextInstanceProfile struct {
	Name     string
	ARN      string
	RoleName string
}

func ensureCloudContextInstanceProfile(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, name string) (cloudContextInstanceProfile, error) {
	roleName := cloudContextInstanceRoleName(name)
	profileName := cloudContextInstanceProfileName(name)
	if roleName == "" || profileName == "" {
		return cloudContextInstanceProfile{}, fmt.Errorf("cloud context name is required")
	}

	trustPath, cleanupTrust, err := cloudContextPolicyFile(ctx, ec2AssumeRolePolicy())
	if err != nil {
		return cloudContextInstanceProfile{}, err
	}
	defer cleanupTrust()
	policyPath, cleanupPolicy, err := cloudContextPolicyFile(ctx, cloudContextSelfStopPolicy(provider, region, name))
	if err != nil {
		return cloudContextInstanceProfile{}, err
	}
	defer cleanupPolicy()

	if ctx.DryRun {
		if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "create-role", "--role-name", roleName, "--assume-role-policy-document", "file://" + trustPath, "--query", "Role.RoleName", "--output", "text"}); err != nil {
			return cloudContextInstanceProfile{}, err
		}
		if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "put-role-policy", "--role-name", roleName, "--policy-name", "erun-self-stop", "--policy-document", "file://" + policyPath}); err != nil {
			return cloudContextInstanceProfile{}, err
		}
		if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "create-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Arn", "--output", "text"}); err != nil {
			return cloudContextInstanceProfile{}, err
		}
		if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "add-role-to-instance-profile", "--instance-profile-name", profileName, "--role-name", roleName}); err != nil {
			return cloudContextInstanceProfile{}, err
		}
		return cloudContextInstanceProfile{
			Name:     profileName,
			ARN:      cloudContextInstanceProfileARN(provider, profileName),
			RoleName: roleName,
		}, nil
	}

	if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "get-role", "--role-name", roleName, "--query", "Role.RoleName", "--output", "text"}); err != nil {
		if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "create-role", "--role-name", roleName, "--assume-role-policy-document", "file://" + trustPath, "--query", "Role.RoleName", "--output", "text"}); err != nil {
			return cloudContextInstanceProfile{}, err
		}
	}
	if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "put-role-policy", "--role-name", roleName, "--policy-name", "erun-self-stop", "--policy-document", "file://" + policyPath}); err != nil {
		return cloudContextInstanceProfile{}, err
	}
	profileARN, err := deps.RunAWS(ctx, provider, region, []string{"iam", "get-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Arn", "--output", "text"})
	createdProfile := false
	if err != nil {
		profileARN, err = deps.RunAWS(ctx, provider, region, []string{"iam", "create-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Arn", "--output", "text"})
		if err != nil {
			return cloudContextInstanceProfile{}, err
		}
		createdProfile = true
	}
	if err := ensureCloudContextInstanceProfileRole(ctx, deps, provider, region, profileName, roleName, createdProfile); err != nil {
		return cloudContextInstanceProfile{}, err
	}

	profileARN = strings.TrimSpace(profileARN)
	if profileARN == "" || strings.EqualFold(profileARN, "none") {
		profileARN = cloudContextInstanceProfileARN(provider, profileName)
	}
	return cloudContextInstanceProfile{
		Name:     profileName,
		ARN:      profileARN,
		RoleName: roleName,
	}, nil
}

func ensureCloudContextInstanceProfileRole(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, profileName, roleName string, createdProfile bool) error {
	if !createdProfile {
		existingRole, err := cloudContextInstanceProfileRoleName(ctx, deps, provider, region, profileName)
		if err != nil {
			return err
		}
		if existingRole == roleName {
			return nil
		}
		if existingRole != "" {
			return fmt.Errorf("instance profile %q already contains role %q; expected %q", profileName, existingRole, roleName)
		}
	}

	if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "add-role-to-instance-profile", "--instance-profile-name", profileName, "--role-name", roleName}); err != nil {
		if !isInstanceProfileRoleLimitError(err) && !isAlreadyAssociatedAWSError(err) {
			return err
		}
		existingRole, roleErr := cloudContextInstanceProfileRoleName(ctx, deps, provider, region, profileName)
		if roleErr != nil {
			return err
		}
		if existingRole == roleName {
			return nil
		}
		if existingRole != "" {
			return fmt.Errorf("instance profile %q already contains role %q; expected %q", profileName, existingRole, roleName)
		}
		return err
	}
	return nil
}

func cloudContextInstanceProfileRoleName(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, profileName string) (string, error) {
	roleName, err := deps.RunAWS(ctx, provider, region, []string{"iam", "get-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Roles[0].RoleName", "--output", "text"})
	if err != nil {
		return "", err
	}
	roleName = strings.TrimSpace(roleName)
	if roleName == "" || strings.EqualFold(roleName, "none") || strings.EqualFold(roleName, "null") {
		return "", nil
	}
	return roleName, nil
}

func ec2AssumeRolePolicy() map[string]any {
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect": "Allow",
			"Principal": map[string]string{
				"Service": "ec2.amazonaws.com",
			},
			"Action": "sts:AssumeRole",
		}},
	}
}

func cloudContextSelfStopPolicy(provider CloudProviderConfig, region, name string) map[string]any {
	accountID := strings.TrimSpace(provider.AccountID)
	if accountID == "" {
		accountID = "*"
	}
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect":   "Allow",
			"Action":   "ec2:StopInstances",
			"Resource": fmt.Sprintf("arn:aws:ec2:%s:%s:instance/*", region, accountID),
			"Condition": map[string]any{
				"StringEquals": map[string]string{
					"ec2:ResourceTag/erun:context": name,
				},
			},
		}},
	}
}

func cloudContextPolicyFile(ctx Context, policy map[string]any) (string, func(), error) {
	if ctx.DryRun {
		return "<generated-iam-policy>", func() {}, nil
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp("", "erun-cloud-context-policy-*.json")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(file.Name())
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return file.Name(), cleanup, nil
}

func cloudContextInstanceRoleName(name string) string {
	return cloudContextInstanceProfileBaseName(name)
}

func cloudContextInstanceProfileName(name string) string {
	return cloudContextInstanceProfileBaseName(name)
}

func cloudContextInstanceProfileBaseName(name string) string {
	name = sanitizeIAMName(name)
	if !strings.HasPrefix(name, "erun-") {
		name = "erun-" + name
	}
	return truncateIAMName(name + "-host-stop")
}

func cloudContextInstanceProfileARN(provider CloudProviderConfig, profileName string) string {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return ""
	}
	accountID := strings.TrimSpace(provider.AccountID)
	if accountID == "" {
		return ""
	}
	return "arn:aws:iam::" + accountID + ":instance-profile/" + profileName
}

func sanitizeIAMName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if isValidIAMNameRune(r) {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func isValidIAMNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' ||
		r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' ||
		strings.ContainsRune("+=,.@_-", r)
}

func truncateIAMName(value string) string {
	value = strings.Trim(value, "-")
	if len(value) <= 64 {
		return value
	}
	return strings.TrimRight(value[:64], "-")
}

func isAlreadyAssociatedAWSError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already") || strings.Contains(message, "limitexceeded")
}

func isInstanceProfileRoleLimitError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "limitexceeded") && strings.Contains(message, "addroletoinstanceprofile")
}

func isExistingInstanceProfileAssociationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "incorrectstate") && strings.Contains(message, "existing association")
}

func describeCloudContextPublicIP(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID string) (string, error) {
	publicIP, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "describe-instances",
		"--instance-ids", instanceID,
		"--query", "Reservations[0].Instances[0].PublicIpAddress",
		"--output", "text",
	})
	if err != nil {
		return "", err
	}
	publicIP = strings.TrimSpace(publicIP)
	if publicIP == "" || strings.EqualFold(publicIP, "none") {
		if ctx.DryRun {
			return "203.0.113.10", nil
		}
		return "", fmt.Errorf("cloud context instance %q does not have a public IP yet", instanceID)
	}
	return publicIP, nil
}

func configureCloudKubeContext(ctx Context, deps CloudContextDependencies, config CloudContextConfig) error {
	config = NormalizeCloudContextConfig(config)
	if config.PublicIP == "" {
		return fmt.Errorf("cloud context public IP is required")
	}
	if config.AdminToken == "" {
		return fmt.Errorf("cloud context admin token is required")
	}
	commands := [][]string{
		{"config", "set-cluster", config.KubernetesContext, "--server", "https://" + config.PublicIP + ":6443", "--insecure-skip-tls-verify=true"},
		{"config", "set-credentials", config.KubernetesContext, "--token", config.AdminToken},
		{"config", "set-context", config.KubernetesContext, "--cluster", config.KubernetesContext, "--user", config.KubernetesContext},
	}
	for _, args := range commands {
		if err := deps.RunKubectl(ctx, args); err != nil {
			return err
		}
	}
	return nil
}

func cloudContextUserDataFile(ctx Context, adminToken string) (string, func(), error) {
	if ctx.DryRun {
		return "<generated-k3s-user-data>", func() {}, nil
	}
	file, err := os.CreateTemp("", "erun-k3s-user-data-*.sh")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(file.Name())
	}
	userData := `#!/bin/sh
set -eu
mkdir -p /etc/rancher/k3s
cat >/etc/rancher/k3s/token-auth.csv <<'EOF'
` + adminToken + `,erun-admin,erun-admin,"system:masters"
EOF
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="server --kube-apiserver-arg=token-auth-file=/etc/rancher/k3s/token-auth.csv" sh -
`
	if _, err := file.WriteString(userData); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return file.Name(), cleanup, nil
}

func saveCloudContextConfig(store CloudContextStore, context CloudContextConfig) error {
	context = NormalizeCloudContextConfig(context)
	config, _, err := store.LoadERunConfig()
	if err == ErrNotInitialized {
		config = ERunConfig{}
	} else if err != nil {
		return err
	}
	config.CloudContexts = upsertCloudContext(config.CloudContexts, context)
	return store.SaveERunConfig(config)
}

func findCloudContext(store CloudReadStore, name string) (CloudContextConfig, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CloudContextConfig{}, false, fmt.Errorf("cloud context name is required")
	}
	contexts, err := ListCloudContexts(store)
	if err != nil {
		return CloudContextConfig{}, false, err
	}
	for _, context := range contexts {
		if context.Name == name || context.KubernetesContext == name {
			return context, true, nil
		}
	}
	return CloudContextConfig{}, false, nil
}

func upsertCloudContext(contexts []CloudContextConfig, context CloudContextConfig) []CloudContextConfig {
	context = NormalizeCloudContextConfig(context)
	updated := false
	result := make([]CloudContextConfig, 0, len(contexts)+1)
	for _, existing := range contexts {
		existing = NormalizeCloudContextConfig(existing)
		if existing.Name == "" {
			continue
		}
		if existing.Name == context.Name {
			result = append(result, context)
			updated = true
			continue
		}
		result = append(result, existing)
	}
	if !updated {
		result = append(result, context)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func normalizedCloudContexts(contexts []CloudContextConfig) []CloudContextConfig {
	result := make([]CloudContextConfig, 0, len(contexts))
	for _, context := range contexts {
		context = NormalizeCloudContextConfig(context)
		if context.Name == "" {
			continue
		}
		result = append(result, context)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func validCloudContextInstanceType(value string) bool {
	for _, option := range CloudContextInstanceTypes() {
		if value == option {
			return true
		}
	}
	return false
}

func validCloudContextDiskSize(value int) bool {
	for _, option := range CloudContextDiskSizesGB() {
		if value == option {
			return true
		}
	}
	return false
}

func validCloudContextRegion(value string) bool {
	for _, option := range CloudContextRegions() {
		if value == option {
			return true
		}
	}
	return false
}

func generatedCloudContextName(provider CloudProviderConfig, region string, existingContexts []CloudContextConfig) string {
	parts := make([]string, 0, 2)
	if provider.AccountID != "" {
		parts = append(parts, provider.AccountID)
	} else if provider.Username != "" {
		parts = append(parts, provider.Username)
	} else {
		parts = append(parts, provider.Alias)
	}
	if region != "" {
		parts = append(parts, region)
	}
	tail := sanitizeCloudContextName(strings.Join(parts, "-"))
	return nextCloudContextName(tail, existingContexts)
}

func nextCloudContextName(tail string, existingContexts []CloudContextConfig) string {
	tail = sanitizeCloudContextName(tail)
	if tail == "" {
		tail = "context"
	}
	prefix := "erun-"
	suffix := "-" + tail
	next := 1
	for _, context := range existingContexts {
		for _, name := range []string{context.Name, context.KubernetesContext} {
			name = strings.TrimSpace(name)
			if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
				continue
			}
			counter := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
			if len(counter) != 3 {
				continue
			}
			value, err := strconv.Atoi(counter)
			if err == nil && value >= next {
				next = value + 1
			}
		}
	}
	return fmt.Sprintf("erun-%03d-%s", next, tail)
}

func sanitizeCloudContextName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func normalizeCloudContextDependencies(deps CloudContextDependencies) CloudContextDependencies {
	if deps.RunAWS == nil {
		deps.RunAWS = defaultRunCloudContextAWS
	}
	if deps.RunKubectl == nil {
		deps.RunKubectl = defaultRunCloudContextKubectl
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Sleep == nil {
		deps.Sleep = time.Sleep
	}
	if deps.NewToken == nil {
		deps.NewToken = newCloudContextToken
	}
	return deps
}

func defaultRunCloudContextAWS(ctx Context, provider CloudProviderConfig, region string, args []string) (string, error) {
	fullArgs := append([]string(nil), args...)
	if region = strings.TrimSpace(region); region != "" && !containsAWSFlag(fullArgs, "--region") {
		fullArgs = append(fullArgs, "--region", region)
	}
	if profile := strings.TrimSpace(provider.Profile); profile != "" && !containsAWSFlag(fullArgs, "--profile") {
		fullArgs = append(fullArgs, "--profile", profile)
	}
	ctx.TraceCommand("", "aws", fullArgs...)
	if ctx.DryRun {
		return dryRunAWSOutput(args), nil
	}
	var stdout bytes.Buffer
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := RawCommandRunner("", "aws", fullArgs, nil, &stdout, stderr); err != nil {
		return "", fmt.Errorf("aws %s: %s", strings.Join(args, " "), commandErrorMessage(err, stderrBuffer.String(), "AWS command failed"))
	}
	return stdout.String(), nil
}

func defaultRunCloudContextKubectl(ctx Context, args []string) error {
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	stdout, _ := captureWriter(ctx.Stdout)
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := RawCommandRunner("", "kubectl", args, nil, stdout, stderr); err != nil {
		return fmt.Errorf("kubectl %s: %s", strings.Join(args, " "), commandErrorMessage(err, stderrBuffer.String(), "kubectl command failed"))
	}
	return nil
}

func containsAWSFlag(args []string, flag string) bool {
	for i, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
		if i > 0 && args[i-1] == flag {
			return true
		}
	}
	return false
}

func dryRunAWSOutput(args []string) string {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "ssm get-parameter"):
		return "ami-<latest-ubuntu-arm64>\n"
	case strings.Contains(joined, "ec2 create-security-group"):
		return "sg-<cloud-context>\n"
	case strings.Contains(joined, "ec2 run-instances"):
		return "i-<new-instance>\n"
	case strings.Contains(joined, "ec2 describe-instances"):
		return "203.0.113.10\n"
	default:
		return ""
	}
}

func newCloudContextToken() string {
	token := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, token); err != nil {
		return "erun-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(token)
}
