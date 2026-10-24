package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func NormalizeCloudDependencies(deps CloudDependencies) CloudDependencies {
	deps = normalizeAWSCloudDependencies(deps)
	deps = normalizeCloudflareCloudDependencies(deps)
	deps = normalizeERunCloudDependencies(deps)
	return deps
}

// normalizeERunCloudDependencies leaves CloudSecretStore nil: only a transport wires one.
func normalizeERunCloudDependencies(deps CloudDependencies) CloudDependencies {
	if deps.FetchPlatformInfo == nil {
		deps.FetchPlatformInfo = defaultFetchPlatformInfo
	}
	if deps.FetchOIDCDiscovery == nil {
		deps.FetchOIDCDiscovery = defaultFetchOIDCDiscovery
	}
	if deps.StartERunDeviceAuthorization == nil {
		deps.StartERunDeviceAuthorization = defaultStartERunDeviceAuthorization
	}
	if deps.PollERunDeviceToken == nil {
		deps.PollERunDeviceToken = pollERunDeviceToken
	}
	if deps.RunERunAuthCodeLogin == nil {
		deps.RunERunAuthCodeLogin = runERunAuthorizationCodeLogin
	}
	if deps.RefreshERunTokens == nil {
		deps.RefreshERunTokens = defaultRefreshERunTokens
	}
	if deps.ClientCredentialsERunTokens == nil {
		deps.ClientCredentialsERunTokens = defaultClientCredentialsERunTokens
	}
	if deps.FetchVersionJSON == nil {
		deps.FetchVersionJSON = defaultFetchVersionJSON
	}
	if deps.ResolveHostAddrs == nil {
		deps.ResolveHostAddrs = defaultResolveHostAddrs
	}
	return deps
}

func normalizeAWSCloudDependencies(deps CloudDependencies) CloudDependencies {
	if deps.RunAWSConfigureSSO == nil {
		deps.RunAWSConfigureSSO = defaultRunAWSConfigureSSO
	}
	if deps.RunAWSLogin == nil {
		deps.RunAWSLogin = defaultRunAWSLogin
	}
	if deps.RunAWSLogout == nil {
		deps.RunAWSLogout = defaultRunAWSLogout
	}
	if deps.RunAWSBearerToken == nil {
		deps.RunAWSBearerToken = runAWSBearerTokenForExecutionMode()
	}
	if deps.RunAWSEnableOIDC == nil {
		deps.RunAWSEnableOIDC = defaultRunAWSEnableOIDC
	}
	if deps.RunAWSExportCredentials == nil {
		deps.RunAWSExportCredentials = runAWSExportCredentialsForExecutionMode()
	}
	if deps.ResolveAWSIdentity == nil {
		deps.ResolveAWSIdentity = resolveAWSIdentityForExecutionMode()
	}
	if deps.CheckAWSStatus == nil {
		deps.CheckAWSStatus = checkAWSStatusForExecutionMode()
	}
	return deps
}

// resolveAWSIdentityForExecutionMode picks the subprocess or library implementation per the aws-sts mode.
func resolveAWSIdentityForExecutionMode() func(core.Context, string) (AWSIdentity, error) {
	if configpkg.CurrentExecutionMode(awsSTSIdentityExecutionOperation) == configpkg.ExecutionModeLibrary {
		return libraryResolveAWSIdentity
	}
	return defaultResolveAWSIdentity
}

func runAWSBearerTokenForExecutionMode() func(core.Context, string, string) (string, error) {
	if configpkg.CurrentExecutionMode(awsSTSWebIdentityTokenExecutionOperation) == configpkg.ExecutionModeLibrary {
		return libraryRunAWSBearerToken
	}
	return defaultRunAWSBearerToken
}

func runAWSExportCredentialsForExecutionMode() func(core.Context, string) (CloudProviderCredentials, error) {
	if configpkg.CurrentExecutionMode(awsExportCredentialsExecutionOperation) == configpkg.ExecutionModeLibrary {
		return libraryRunAWSExportCredentials
	}
	return defaultRunAWSExportCredentials
}

func checkAWSStatusForExecutionMode() func(core.Context, configpkg.CloudProviderConfig) CloudProviderStatus {
	if configpkg.CurrentExecutionMode(awsSTSIdentityExecutionOperation) == configpkg.ExecutionModeLibrary {
		return libraryCheckAWSStatus
	}
	return defaultCheckAWSStatus
}

// normalizeCloudflareCloudDependencies leaves CloudSecretStore nil: only a transport wires one.
func normalizeCloudflareCloudDependencies(deps CloudDependencies) CloudDependencies {
	if deps.VerifyCloudflareToken == nil {
		deps.VerifyCloudflareToken = defaultVerifyCloudflareToken
	}
	if deps.ListCloudflareAccounts == nil {
		deps.ListCloudflareAccounts = defaultListCloudflareAccounts
	}
	return deps
}

func defaultRunAWSConfigureSSO(ctx core.Context, config AWSProfileConfig) error {
	config = normalizeAWSProfileConfig(config)
	if err := validateAWSProfileConfig(config); err != nil {
		return err
	}
	settings := []struct {
		key   string
		value string
	}{
		{key: "sso_start_url", value: config.SSOStartURL},
		{key: "sso_region", value: config.SSORegion},
		{key: "sso_account_id", value: config.AccountID},
		{key: "sso_role_name", value: config.RoleName},
		{key: "region", value: config.Region},
		{key: "output", value: "json"},
	}
	for _, setting := range settings {
		args := []string{"configure", "set", setting.key, setting.value, "--profile", config.Profile}
		ctx.TraceCommand("", "aws", args...)
		if ctx.DryRun {
			continue
		}
		stdout, _ := captureWriter(ctx.Stdout)
		stderr, stderrBuffer := captureWriter(ctx.Stderr)
		if err := core.RawCommandRunner("", "aws", args, nil, stdout, stderr); err != nil {
			return fmt.Errorf("aws configure set %s: %s", setting.key, commandErrorMessage(err, stderrBuffer.String(), "AWS SSO setup failed"))
		}
	}
	return nil
}

func normalizeAWSProfileConfig(config AWSProfileConfig) AWSProfileConfig {
	config.Profile = strings.TrimSpace(config.Profile)
	config.SSOStartURL = strings.TrimSpace(config.SSOStartURL)
	config.SSORegion = strings.TrimSpace(config.SSORegion)
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.RoleName = strings.TrimSpace(config.RoleName)
	config.Region = strings.TrimSpace(config.Region)
	if config.Region == "" {
		config.Region = config.SSORegion
	}
	return config
}

func validateAWSProfileConfig(config AWSProfileConfig) error {
	switch {
	case config.Profile == "":
		return fmt.Errorf("AWS profile name is required")
	case config.SSOStartURL == "":
		return fmt.Errorf("AWS SSO start URL is required")
	case config.SSORegion == "":
		return fmt.Errorf("AWS SSO region is required")
	case config.AccountID == "":
		return fmt.Errorf("AWS account ID is required")
	case config.RoleName == "":
		return fmt.Errorf("AWS permission set is required")
	case config.Region == "":
		return fmt.Errorf("AWS region is required")
	default:
		return nil
	}
}

func defaultRunAWSLogin(ctx core.Context, profile string) error {
	args := []string{"sso", "login"}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return nil
	}
	stdout, _ := captureWriter(ctx.Stdout)
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := core.RawCommandRunner("", "aws", args, ctx.Stdin, stdout, stderr); err != nil {
		return fmt.Errorf("aws sso login: %s", commandErrorMessage(err, stderrBuffer.String(), "AWS SSO login failed"))
	}
	return nil
}

func defaultRunAWSLogout(ctx core.Context, profile string) error {
	args := []string{"sso", "logout"}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return nil
	}
	stdout, _ := captureWriter(ctx.Stdout)
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := core.RawCommandRunner("", "aws", args, ctx.Stdin, stdout, stderr); err != nil {
		return fmt.Errorf("aws sso logout: %s", commandErrorMessage(err, stderrBuffer.String(), "AWS SSO logout failed"))
	}
	return nil
}

func defaultRunAWSBearerToken(ctx core.Context, profile, audience string) (string, error) {
	audience = normalizeCloudBearerAudience(audience)
	args := awsGetWebIdentityTokenArgs(profile, audience)
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return "", nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := core.RawCommandRunner("", "aws", args, nil, &stdout, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("get AWS web identity token: %s", message)
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", fmt.Errorf("get AWS web identity token: empty token")
	}
	return token, nil
}

func defaultRunAWSExportCredentials(ctx core.Context, profile string) (CloudProviderCredentials, error) {
	args := awsConfigureExportCredentialsArgs(profile)
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return CloudProviderCredentials{}, nil
	}
	var stdout, stderr bytes.Buffer
	if err := core.RawCommandRunner("", "aws", args, nil, &stdout, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return CloudProviderCredentials{}, fmt.Errorf("export AWS credentials: %s", message)
	}
	return parseAWSExportCredentials(stdout.Bytes())
}

func parseAWSExportCredentials(raw []byte) (CloudProviderCredentials, error) {
	var payload struct {
		Version         int    `json:"Version"`
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		SessionToken    string `json:"SessionToken"`
		Expiration      string `json:"Expiration"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &payload); err != nil {
		return CloudProviderCredentials{}, fmt.Errorf("parse AWS exported credentials: %w", err)
	}
	if payload.AccessKeyID == "" || payload.SecretAccessKey == "" {
		return CloudProviderCredentials{}, fmt.Errorf("AWS exported credentials are missing access key id or secret")
	}
	creds := CloudProviderCredentials{
		AccessKeyID:     payload.AccessKeyID,
		SecretAccessKey: payload.SecretAccessKey,
		SessionToken:    payload.SessionToken,
	}
	if expiration := strings.TrimSpace(payload.Expiration); expiration != "" {
		parsed, err := time.Parse(time.RFC3339, expiration)
		if err != nil {
			return CloudProviderCredentials{}, fmt.Errorf("parse AWS credential expiration %q: %w", expiration, err)
		}
		creds.Expiration = parsed
	}
	return creds, nil
}

func defaultRunAWSEnableOIDC(ctx core.Context, profile string) (string, error) {
	args := []string{
		"iam", "enable-outbound-web-identity-federation",
		"--query", "IssuerIdentifier",
		"--output", "text",
	}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
	if ctx.DryRun {
		return "", nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := core.RawCommandRunner("", "aws", args, nil, &stdout, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		if isAWSOutboundWebIdentityFederationAlreadyEnabledMessage(message) {
			return "", nil
		}
		if isAWSAccessDeniedMessage(message) {
			return "", fmt.Errorf("enable AWS outbound web identity federation requires the iam:EnableOutboundWebIdentityFederation permission: %s", message)
		}
		return "", fmt.Errorf("enable AWS outbound web identity federation: %s", message)
	}
	return normalizeOIDCIssuerURL(stdout.String()), nil
}

func isAWSAccessDeniedMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "accessdenied") ||
		strings.Contains(message, "access denied") ||
		strings.Contains(message, "unauthorizedaccess") ||
		strings.Contains(message, "not authorized to perform")
}

func isAWSOutboundWebIdentityFederationDisabled(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "outboundwebidentityfederationdisabledexception") ||
		strings.Contains(message, "outboundwebidentityfederation is disabled") ||
		strings.Contains(message, "outbound web identity federation is disabled")
}

func isAWSOutboundWebIdentityFederationAlreadyEnabledMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "featureenabled") ||
		strings.Contains(message, "outbound identity federation is already enabled") ||
		strings.Contains(message, "outbound web identity federation is already enabled")
}

func generatedAWSProfileName() string {
	return "erun-sso-" + time.Now().UTC().Format("20060102150405")
}

func defaultResolveAWSIdentity(ctx core.Context, profile string) (AWSIdentity, error) {
	args := awsGetCallerIdentityArgs(profile)
	ctx.TraceCommand("", "aws", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := core.RawCommandRunner("", "aws", args, nil, &stdout, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return AWSIdentity{}, fmt.Errorf("resolve AWS identity: %s", message)
	}
	var identity AWSIdentity
	if err := json.Unmarshal(stdout.Bytes(), &identity); err != nil {
		return AWSIdentity{}, fmt.Errorf("parse AWS identity: %w", err)
	}
	return identity, nil
}

func defaultCheckAWSStatus(_ core.Context, provider configpkg.CloudProviderConfig) CloudProviderStatus {
	args := []string{"sts", "get-caller-identity", "--output", "json"}
	if provider.Profile != "" {
		args = append(args, "--profile", provider.Profile)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := core.RawCommandRunner("", "aws", args, nil, &stdout, &stderr)
	if err == nil {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusActive}
	}
	message := strings.TrimSpace(stderr.String())
	if message == "" {
		message = strings.TrimSpace(err.Error())
	}
	if errors.Is(err, exec.ErrNotFound) {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusUnknown, Message: "aws CLI is not installed"}
	}
	status := configpkg.CloudTokenStatusExpired
	if strings.Contains(strings.ToLower(message), "could not be found") || strings.Contains(strings.ToLower(message), "not found") {
		status = configpkg.CloudTokenStatusNotConfigured
	}
	return CloudProviderStatus{CloudProviderConfig: provider, Status: status, Message: message}
}

func captureWriter(writer io.Writer) (io.Writer, *bytes.Buffer) {
	buffer := new(bytes.Buffer)
	if writer == nil {
		return buffer, buffer
	}
	return io.MultiWriter(writer, buffer), buffer
}

func commandErrorMessage(err error, stderr, fallback string) string {
	message := strings.TrimSpace(stderr)
	if message != "" {
		return message
	}
	if fallback != "" {
		return fallback + ": " + err.Error()
	}
	return err.Error()
}
