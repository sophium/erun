package contextinstance

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
)

type cloudContextInstanceProfile struct {
	Name     string
	ARN      string
	RoleName string
}

type cloudContextPolicyPaths struct {
	Trust  string
	Policy string
}

func EnsureCloudContextInstanceProfile(ctx core.Context, deps cloudcontext.CloudContextDependencies, provider model.CloudProviderConfig, region, name string) (cloudContextInstanceProfile, error) {
	roleName := cloudContextInstanceRoleName(name)
	profileName := cloudContextInstanceProfileName(name)
	if roleName == "" || profileName == "" {
		return cloudContextInstanceProfile{}, fmt.Errorf("cloud context name is required")
	}

	policies, cleanup, err := cloudContextInstanceProfilePolicyPaths(ctx, provider, region, name)
	if err != nil {
		return cloudContextInstanceProfile{}, err
	}
	defer cleanup()

	if ctx.DryRun {
		return dryRunCloudContextInstanceProfile(ctx, deps, provider, region, profileName, roleName, policies)
	}
	return realCloudContextInstanceProfile(ctx, deps, provider, region, profileName, roleName, policies)
}

func cloudContextInstanceProfilePolicyPaths(ctx core.Context, provider model.CloudProviderConfig, region, name string) (cloudContextPolicyPaths, func(), error) {
	trustPath, cleanupTrust, err := cloudContextPolicyFile(ctx, ec2AssumeRolePolicy())
	if err != nil {
		return cloudContextPolicyPaths{}, func() {}, err
	}
	policyPath, cleanupPolicy, err := cloudContextPolicyFile(ctx, cloudContextSelfStopPolicy(provider, region, name))
	if err != nil {
		cleanupTrust()
		return cloudContextPolicyPaths{}, func() {}, err
	}
	cleanup := func() {
		cleanupPolicy()
		cleanupTrust()
	}
	return cloudContextPolicyPaths{Trust: trustPath, Policy: policyPath}, cleanup, nil
}

func dryRunCloudContextInstanceProfile(ctx core.Context, deps cloudcontext.CloudContextDependencies, provider model.CloudProviderConfig, region, profileName, roleName string, policies cloudContextPolicyPaths) (cloudContextInstanceProfile, error) {
	commands := [][]string{
		{"iam", "create-role", "--role-name", roleName, "--assume-role-policy-document", "file://" + policies.Trust, "--query", "Role.RoleName", "--output", "text"},
		{"iam", "put-role-policy", "--role-name", roleName, "--policy-name", "erun-self-stop", "--policy-document", "file://" + policies.Policy},
		{"iam", "create-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Arn", "--output", "text"},
		{"iam", "add-role-to-instance-profile", "--instance-profile-name", profileName, "--role-name", roleName},
	}
	for _, command := range commands {
		if _, err := deps.RunAWS(ctx, provider, region, command); err != nil {
			return cloudContextInstanceProfile{}, err
		}
	}
	return cloudContextInstanceProfile{Name: profileName, ARN: cloudContextInstanceProfileARN(provider, profileName), RoleName: roleName}, nil
}

func realCloudContextInstanceProfile(ctx core.Context, deps cloudcontext.CloudContextDependencies, provider model.CloudProviderConfig, region, profileName, roleName string, policies cloudContextPolicyPaths) (cloudContextInstanceProfile, error) {
	if err := ensureCloudContextInstanceRole(ctx, deps, provider, region, roleName, policies.Trust); err != nil {
		return cloudContextInstanceProfile{}, err
	}
	if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "put-role-policy", "--role-name", roleName, "--policy-name", "erun-self-stop", "--policy-document", "file://" + policies.Policy}); err != nil {
		return cloudContextInstanceProfile{}, err
	}
	profileARN, createdProfile, err := ensureCloudContextInstanceProfileExists(ctx, deps, provider, region, profileName)
	if err != nil {
		return cloudContextInstanceProfile{}, err
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

func ensureCloudContextInstanceRole(ctx core.Context, deps cloudcontext.CloudContextDependencies, provider model.CloudProviderConfig, region, roleName, trustPath string) error {
	if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "get-role", "--role-name", roleName, "--query", "Role.RoleName", "--output", "text"}); err == nil {
		return nil
	}
	_, err := deps.RunAWS(ctx, provider, region, []string{"iam", "create-role", "--role-name", roleName, "--assume-role-policy-document", "file://" + trustPath, "--query", "Role.RoleName", "--output", "text"})
	return err
}

func ensureCloudContextInstanceProfileExists(ctx core.Context, deps cloudcontext.CloudContextDependencies, provider model.CloudProviderConfig, region, profileName string) (string, bool, error) {
	profileARN, err := deps.RunAWS(ctx, provider, region, []string{"iam", "get-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Arn", "--output", "text"})
	if err == nil {
		return profileARN, false, nil
	}
	profileARN, err = deps.RunAWS(ctx, provider, region, []string{"iam", "create-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Arn", "--output", "text"})
	return profileARN, true, err
}

func ensureCloudContextInstanceProfileRole(ctx core.Context, deps cloudcontext.CloudContextDependencies, provider model.CloudProviderConfig, region, profileName, roleName string, createdProfile bool) error {
	addRole, err := shouldAddCloudContextProfileRole(ctx, deps, provider, region, profileName, roleName, createdProfile)
	if err != nil {
		return err
	}
	if !addRole {
		return nil
	}

	if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "add-role-to-instance-profile", "--instance-profile-name", profileName, "--role-name", roleName}); err != nil {
		if !isInstanceProfileRoleLimitError(err) && !IsAlreadyAssociatedAWSError(err) {
			return err
		}
		ctx.Trace("cloud-context: instance profile " + profileName + " already carries a role — checking it matches " + roleName)
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

func shouldAddCloudContextProfileRole(ctx core.Context, deps cloudcontext.CloudContextDependencies, provider model.CloudProviderConfig, region, profileName, roleName string, createdProfile bool) (bool, error) {
	if createdProfile {
		return true, nil
	}
	existingRole, err := cloudContextInstanceProfileRoleName(ctx, deps, provider, region, profileName)
	if err != nil {
		return false, err
	}
	if existingRole == roleName {
		return false, nil
	}
	if existingRole != "" {
		return false, fmt.Errorf("instance profile %q already contains role %q; expected %q", profileName, existingRole, roleName)
	}
	return true, nil
}

func cloudContextInstanceProfileRoleName(ctx core.Context, deps cloudcontext.CloudContextDependencies, provider model.CloudProviderConfig, region, profileName string) (string, error) {
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

func cloudContextSelfStopPolicy(provider model.CloudProviderConfig, region, name string) map[string]any {
	accountID := strings.TrimSpace(provider.AccountID)
	if accountID == "" {
		accountID = "*"
	}
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Sid":      "AllowSelfStop",
				"Effect":   "Allow",
				"Action":   "ec2:StopInstances",
				"Resource": fmt.Sprintf("arn:aws:ec2:%s:%s:instance/*", region, accountID),
				"Condition": map[string]any{
					"StringEquals": map[string]string{
						"ec2:ResourceTag/erun:context": name,
					},
				},
			},
			{
				"Sid":    "AllowBedrockClaudeCode",
				"Effect": "Allow",
				"Action": []string{
					"bedrock:InvokeModel",
					"bedrock:InvokeModelWithResponseStream",
					"bedrock:ListInferenceProfiles",
					"bedrock:GetInferenceProfile",
				},
				"Resource": []string{
					"arn:aws:bedrock:*:*:inference-profile/*",
					"arn:aws:bedrock:*:*:application-inference-profile/*",
					"arn:aws:bedrock:*:*:foundation-model/*",
				},
			},
			{
				"Sid":    "AllowBedrockMarketplaceAccess",
				"Effect": "Allow",
				"Action": []string{
					"aws-marketplace:ViewSubscriptions",
					"aws-marketplace:Subscribe",
				},
				"Resource": "*",
				"Condition": map[string]any{
					"StringEquals": map[string]string{
						"aws:CalledViaLast": "bedrock.amazonaws.com",
					},
				},
			},
		},
	}
}

func cloudContextPolicyFile(ctx core.Context, policy map[string]any) (string, func(), error) {
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

func cloudContextInstanceProfileARN(provider model.CloudProviderConfig, profileName string) string {
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

func IsAlreadyAssociatedAWSError(err error) bool {
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

func IsExistingInstanceProfileAssociationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "incorrectstate") && strings.Contains(message, "existing association")
}

func IsDuplicateSecurityGroupError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "invalidgroup.duplicate")
}

func IsDuplicateSecurityGroupPermissionError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "invalidpermission.duplicate")
}
