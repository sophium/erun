package cloud

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// CloudContextStopProtectionParams identifies the context whose stop protection to set.
type CloudContextStopProtectionParams struct {
	Name    string
	Enabled bool
}

// SetCloudContextStopProtection makes every stop be refused until cleared, to keep an
// instance up while repairing it.
func SetCloudContextStopProtection(ctx core.Context, store CloudContextStore, params CloudContextStopProtectionParams, deps CloudContextDependencies) (configpkg.CloudContextStatus, error) {
	if store == nil {
		return configpkg.CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	verb := "disable"
	if !params.Enabled {
		verb = "enable"
	}
	ctx.Trace(fmt.Sprintf("cloud-context stop-protection %s: %s", verb, strings.TrimSpace(params.Name)))
	config, ok, err := findCloudContext(store, params.Name)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	if !ok {
		return configpkg.CloudContextStatus{}, fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(params.Name))
	}
	if config.InstanceID == "" {
		return configpkg.CloudContextStatus{}, fmt.Errorf("cloud context %q has no instance ID", config.Name)
	}
	provider, err := ResolveCloudProvider(store, config.CloudProviderAlias)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	flag := "--disable-api-stop"
	if !params.Enabled {
		flag = "--no-disable-api-stop"
	}
	if _, err := deps.RunAWS(ctx, provider, config.Region, []string{"ec2", "modify-instance-attribute", "--instance-id", config.InstanceID, flag}); err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	return configpkg.CloudContextStatus{
		CloudContextConfig:  configpkg.NormalizeCloudContextConfig(config),
		StopProtection:      params.Enabled,
		StopProtectionKnown: true,
	}, nil
}

// DescribeCloudContextStopProtection reads one context's live stop protection; it costs
// one AWS call, so it is kept out of the bulk refresh.
func DescribeCloudContextStopProtection(ctx core.Context, store CloudContextStore, name string, deps CloudContextDependencies) (configpkg.CloudContextStatus, error) {
	if store == nil {
		return configpkg.CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	config, ok, err := findCloudContext(store, name)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	if !ok {
		return configpkg.CloudContextStatus{}, fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(name))
	}
	if config.InstanceID == "" {
		return configpkg.CloudContextStatus{}, fmt.Errorf("cloud context %q has no instance ID", config.Name)
	}
	provider, err := ResolveCloudProvider(store, config.CloudProviderAlias)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	out, err := deps.RunAWS(ctx, provider, config.Region, []string{
		"ec2", "describe-instance-attribute",
		"--instance-id", config.InstanceID,
		"--attribute", "disableApiStop",
		"--query", "DisableApiStop.Value",
		"--output", "text",
	})
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	enabled := parseCloudContextStopProtectionOutput(out)
	return configpkg.CloudContextStatus{
		CloudContextConfig:  configpkg.NormalizeCloudContextConfig(config),
		StopProtection:      enabled,
		StopProtectionKnown: true,
	}, nil
}

// parseCloudContextStopProtectionOutput treats anything but "true" as disabled.
func parseCloudContextStopProtectionOutput(out string) bool {
	switch strings.ToLower(strings.TrimSpace(out)) {
	case "true":
		return true
	default:
		return false
	}
}

func ensureCloudContextHostStopProfileAssociation(ctx core.Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) error {
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

func ensureCloudContextInstanceProfileAssociation(ctx core.Context, deps CloudContextDependencies, provider configpkg.CloudProviderConfig, region, instanceID, profileRef string) error {
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
		ctx.Trace("cloud-context: instance profile already associated with " + instanceID + " — reusing the existing association")
	}
	return nil
}

func activeCloudContextInstanceProfileAssociationID(ctx core.Context, deps CloudContextDependencies, provider configpkg.CloudProviderConfig, region, instanceID string) (string, error) {
	return describeCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, []string{
		"Name=instance-id,Values=" + instanceID,
		"Name=state,Values=associated",
	})
}

func activeCloudContextInstanceProfileAssociationARN(ctx core.Context, deps CloudContextDependencies, provider configpkg.CloudProviderConfig, region, instanceID string) (string, error) {
	return describeCloudContextInstanceProfileAssociationARN(ctx, deps, provider, region, []string{
		"Name=instance-id,Values=" + instanceID,
		"Name=state,Values=associated",
	})
}

func pendingCloudContextInstanceProfileAssociationID(ctx core.Context, deps CloudContextDependencies, provider configpkg.CloudProviderConfig, region, instanceID string) (string, error) {
	return describeCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, []string{
		"Name=instance-id,Values=" + instanceID,
		"Name=state,Values=associating,disassociating",
	})
}

func describeCloudContextInstanceProfileAssociationID(ctx core.Context, deps CloudContextDependencies, provider configpkg.CloudProviderConfig, region string, filters []string) (string, error) {
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

func describeCloudContextInstanceProfileAssociationARN(ctx core.Context, deps CloudContextDependencies, provider configpkg.CloudProviderConfig, region string, filters []string) (string, error) {
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

func replaceCloudContextInstanceProfileAssociation(ctx core.Context, deps CloudContextDependencies, provider configpkg.CloudProviderConfig, region, instanceID, associationID, profileRef string) error {
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
