package cloud

import (
	"fmt"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
)

func InitCloudContext(ctx core.Context, store seam.CloudContextStore, params model.InitCloudContextParams, deps CloudContextDependencies) (model.CloudContextStatus, error) {
	if store == nil {
		return model.CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	ctx.Trace(fmt.Sprintf("cloud-context init: alias=%s region=%s instance-type=%s disk=%dGB/%s",
		strings.TrimSpace(params.CloudProviderAlias),
		strings.TrimSpace(params.Region),
		strings.TrimSpace(params.InstanceType),
		params.DiskSizeGB,
		strings.TrimSpace(params.DiskType)))
	provider, config, err := initCloudContextConfig(store, params, deps)
	if err != nil {
		ctx.Trace("cloud-context init: configuration resolution failed: " + err.Error())
		return model.CloudContextStatus{}, err
	}
	ctx.Trace(fmt.Sprintf("cloud-context init: resolved name=%s kube-context=%s", config.Name, config.KubernetesContext))
	if err := prepareInitCloudContextResources(ctx, deps, provider, params, &config); err != nil {
		return model.CloudContextStatus{}, err
	}

	instanceID, err := runInitCloudContextInstance(ctx, deps, provider, params, &config)
	if err != nil {
		return model.CloudContextStatus{}, err
	}
	config.InstanceID = strings.TrimSpace(instanceID)
	if config.InstanceID == "" {
		config.InstanceID = "i-<new-instance>"
	}

	if err := finalizeInitCloudContext(ctx, deps, provider, &config); err != nil {
		return model.CloudContextStatus{}, err
	}
	status := CloudContextStatusRunning
	if ctx.DryRun {
		return model.CloudContextStatus{CloudContextConfig: configpkg.NormalizeCloudContextConfig(config), Status: status}, nil
	}
	if err := saveCloudContextConfig(store, config); err != nil {
		return model.CloudContextStatus{}, err
	}
	return model.CloudContextStatus{CloudContextConfig: configpkg.NormalizeCloudContextConfig(config), Status: status}, nil
}

func initCloudContextConfig(store seam.CloudContextStore, params model.InitCloudContextParams, deps CloudContextDependencies) (model.CloudProviderConfig, model.CloudContextConfig, error) {
	provider, err := ResolveCloudProvider(store, params.CloudProviderAlias)
	if err != nil {
		return model.CloudProviderConfig{}, model.CloudContextConfig{}, err
	}
	if provider.Provider != configpkg.CloudProviderAWS {
		return model.CloudProviderConfig{}, model.CloudContextConfig{}, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
	existingContexts, err := configpkg.ListCloudContexts(store)
	if err != nil {
		return model.CloudProviderConfig{}, model.CloudContextConfig{}, err
	}
	config, err := resolveInitCloudContextConfig(provider, params, deps.Now(), existingContexts)
	if err != nil {
		return model.CloudProviderConfig{}, model.CloudContextConfig{}, err
	}
	if err := ensureInitCloudContextDoesNotExist(store, config.Name); err != nil {
		return model.CloudProviderConfig{}, model.CloudContextConfig{}, err
	}
	return provider, config, nil
}

func ensureInitCloudContextDoesNotExist(store seam.CloudContextStore, name string) error {
	existing, ok, err := findCloudContext(store, name)
	if err != nil {
		return err
	}
	if ok && existing.InstanceID != "" {
		return fmt.Errorf("cloud context %q already exists", name)
	}
	return nil
}

func prepareInitCloudContextResources(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, params model.InitCloudContextParams, config *model.CloudContextConfig) error {
	securityGroupID, err := initCloudContextSecurityGroup(ctx, deps, provider, params, *config)
	if err != nil {
		return err
	}
	config.SecurityGroupID = securityGroupID
	instanceProfile, err := ensureCloudContextInstanceProfile(ctx, deps, provider, config.Region, config.Name)
	if err != nil {
		return err
	}
	config.InstanceProfileName = instanceProfile.Name
	config.InstanceProfileARN = instanceProfile.ARN
	config.InstanceRoleName = instanceProfile.RoleName
	return nil
}

func initCloudContextSecurityGroup(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, params model.InitCloudContextParams, config model.CloudContextConfig) (string, error) {
	securityGroupID := strings.TrimSpace(params.SecurityGroupID)
	if securityGroupID != "" {
		return securityGroupID, nil
	}
	return createCloudContextSecurityGroup(ctx, deps, provider, config.Region, config.Name)
}

func runInitCloudContextInstance(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, params model.InitCloudContextParams, config *model.CloudContextConfig) (string, error) {
	// Reuse an instance already tagged for this context so a re-run does not launch a duplicate;
	// a deterministically derived admin token still matches the reused instance's baked one.
	existingID, err := findRunningCloudContextInstance(ctx, deps, provider, config.Region, config.Name)
	if err != nil {
		return "", err
	}
	config.AdminToken = deps.NewToken()
	if existingID != "" {
		ctx.Trace("cloud-context init: reusing existing instance " + existingID + " for context " + config.Name)
		return existingID, nil
	}
	ami, err := initCloudContextAMI(ctx, deps, provider, config.Region)
	if err != nil {
		return "", err
	}
	userDataPath, cleanup, err := cloudContextUserDataFile(ctx, config.AdminToken)
	if err != nil {
		return "", err
	}
	defer cleanup()
	args := initCloudContextRunArgs(ami, userDataPath, params, *config)
	return runAWSWithIAMConsistencyRetry(ctx, deps, provider, config.Region, args)
}

func findRunningCloudContextInstance(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, region, name string) (string, error) {
	out, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "describe-instances",
		"--filters",
		"Name=tag:erun:context,Values=" + name,
		"Name=instance-state-name,Values=pending,running",
		"--query", "Reservations[0].Instances[0].InstanceId",
		"--output", "text",
	})
	if err != nil {
		return "", err
	}
	// Dry-run ignores the stubbed lookup so the plan shows a full launch.
	if ctx.DryRun {
		return "", nil
	}
	id := strings.TrimSpace(out)
	if id == "" || strings.EqualFold(id, "none") {
		return "", nil
	}
	return id, nil
}

const iamConsistencyMaxAttempts = 6

var iamConsistencyBackoff = []time.Duration{
	2 * time.Second,
	4 * time.Second,
	6 * time.Second,
	8 * time.Second,
	10 * time.Second,
}

func runAWSWithIAMConsistencyRetry(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, region string, args []string) (string, error) {
	var (
		out string
		err error
	)
	for attempt := 0; attempt < iamConsistencyMaxAttempts; attempt++ {
		out, err = deps.RunAWS(ctx, provider, region, args)
		if err == nil {
			return out, nil
		}
		if !isIAMInstanceProfileConsistencyError(err) || attempt == iamConsistencyMaxAttempts-1 {
			return out, err
		}
		ctx.Trace(fmt.Sprintf("IAM instance profile not yet visible to EC2; retrying in %s", iamConsistencyBackoff[attempt]))
		deps.Sleep(iamConsistencyBackoff[attempt])
	}
	return out, err
}

func isIAMInstanceProfileConsistencyError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Invalid IAM Instance Profile name") ||
		strings.Contains(message, "Invalid IAM Instance Profile ARN")
}

func initCloudContextAMI(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, region string) (string, error) {
	ami, err := deps.RunAWS(ctx, provider, region, []string{
		"ssm", "get-parameter",
		"--name", "/aws/service/canonical/ubuntu/server/24.04/stable/current/arm64/hvm/ebs-gp3/ami-id",
		"--query", "Parameter.Value",
		"--output", "text",
	})
	if err != nil {
		return "", err
	}
	if ami = strings.TrimSpace(ami); ami != "" {
		return ami, nil
	}
	return "ami-<latest-ubuntu-arm64>", nil
}

func initCloudContextRunArgs(ami, userDataPath string, params model.InitCloudContextParams, config model.CloudContextConfig) []string {
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
	runArgs = appendOptionalCloudContextRunArg(runArgs, "--security-group-ids", config.SecurityGroupID)
	runArgs = appendCloudContextProfileRunArg(runArgs, config)
	runArgs = appendOptionalCloudContextRunArg(runArgs, "--subnet-id", params.SubnetID)
	return appendOptionalCloudContextRunArg(runArgs, "--key-name", params.KeyName)
}

func appendOptionalCloudContextRunArg(args []string, name, value string) []string {
	if value = strings.TrimSpace(value); value != "" {
		return append(args, name, value)
	}
	return args
}

func appendCloudContextProfileRunArg(args []string, config model.CloudContextConfig) []string {
	if config.InstanceProfileName != "" {
		return append(args, "--iam-instance-profile", "Name="+config.InstanceProfileName)
	}
	if config.InstanceProfileARN != "" {
		return append(args, "--iam-instance-profile", "Arn="+config.InstanceProfileARN)
	}
	return args
}

func finalizeInitCloudContext(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, config *model.CloudContextConfig) error {
	if _, err := deps.RunAWS(ctx, provider, config.Region, []string{"ec2", "wait", "instance-running", "--instance-ids", config.InstanceID}); err != nil {
		return err
	}
	publicIP, err := describeCloudContextPublicIP(ctx, deps, provider, config.Region, config.InstanceID)
	if err != nil {
		return err
	}
	config.PublicIP = publicIP
	if err := configureCloudKubeContext(ctx, deps, *config); err != nil {
		return err
	}
	config.UpdatedAt = deps.Now().UTC().Format(time.RFC3339)
	return nil
}

func resolveInitCloudContextConfig(provider model.CloudProviderConfig, params model.InitCloudContextParams, now time.Time, existingContexts []model.CloudContextConfig) (model.CloudContextConfig, error) {
	config := model.CloudContextConfig{
		Name:               strings.TrimSpace(params.Name),
		Provider:           configpkg.CloudProviderAWS,
		CloudProviderAlias: provider.Alias,
		Region:             strings.TrimSpace(params.Region),
		InstanceType:       strings.TrimSpace(params.InstanceType),
		DiskType:           strings.TrimSpace(params.DiskType),
		DiskSizeGB:         params.DiskSizeGB,
		CreatedAt:          now.UTC().Format(time.RFC3339),
		UpdatedAt:          now.UTC().Format(time.RFC3339),
	}
	if config.Region == "" {
		config.Region = DefaultCloudContextRegion
	}
	if config.Name == "" {
		config.Name = generatedCloudContextName(provider, config.Region, existingContexts)
	}
	config = configpkg.NormalizeCloudContextConfig(config)
	if config.CloudProviderAlias == "" {
		return model.CloudContextConfig{}, fmt.Errorf("cloud provider alias is required")
	}
	if config.Region == "" {
		return model.CloudContextConfig{}, fmt.Errorf("cloud context region is required")
	}
	if !validCloudContextRegion(config.Region) {
		return model.CloudContextConfig{}, fmt.Errorf("unsupported cloud context region %q", config.Region)
	}
	if !validCloudContextInstanceType(config.InstanceType) {
		return model.CloudContextConfig{}, fmt.Errorf("unsupported cloud context instance type %q", config.InstanceType)
	}
	if !validCloudContextDiskSize(config.DiskSizeGB) {
		return model.CloudContextConfig{}, fmt.Errorf("unsupported cloud context disk size %d", config.DiskSizeGB)
	}
	if config.DiskType != configpkg.DefaultCloudContextDiskType {
		return model.CloudContextConfig{}, fmt.Errorf("unsupported cloud context disk type %q", config.DiskType)
	}
	return config, nil
}

func createCloudContextSecurityGroup(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, region, name string) (string, error) {
	groupName := name + "-k3s"
	groupID, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "create-security-group",
		"--group-name", groupName,
		"--description", "ERun managed k3s API access for " + name,
		"--query", "GroupId",
		"--output", "text",
	})
	if err != nil {
		if !isDuplicateSecurityGroupError(err) {
			return "", err
		}
		ctx.Trace("cloud-context: security group " + groupName + " already exists — reusing it")
		groupID, err = describeCloudContextSecurityGroupID(ctx, deps, provider, region, groupName)
		if err != nil {
			return "", err
		}
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
	if err != nil {
		if !isDuplicateSecurityGroupPermissionError(err) {
			return "", err
		}
		ctx.Trace("cloud-context: k3s API ingress rule already present on " + groupID + " — reusing it")
	}
	return groupID, nil
}

func describeCloudContextSecurityGroupID(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, region, groupName string) (string, error) {
	return deps.RunAWS(ctx, provider, region, []string{
		"ec2", "describe-security-groups",
		"--group-names", groupName,
		"--query", "SecurityGroups[0].GroupId",
		"--output", "text",
	})
}
