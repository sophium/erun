package cloud

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// awsSTSIdentityExecutionOperation keys the `aws sts get-caller-identity` call sites.
// sso login/logout, configure set, and federation setup stay subprocess-only.
const awsSTSIdentityExecutionOperation = "aws-sts"

// awsSTSWebIdentityTokenExecutionOperation keys the `aws sts get-web-identity-token` call site.
const awsSTSWebIdentityTokenExecutionOperation = "aws-sts-web-identity-token"

// awsExportCredentialsExecutionOperation keys the `aws configure export-credentials` call site.
const awsExportCredentialsExecutionOperation = "aws-export-credentials"

// libraryResolveAWSIdentity traces the same CLI line for parity, then uses aws-sdk-go-v2.
func libraryResolveAWSIdentity(ctx core.Context, profile string) (AWSIdentity, error) {
	profile = strings.TrimSpace(profile)
	traceAWSGetCallerIdentity(ctx, profile)
	identity, err := awsSDKCallerIdentity(context.Background(), profile)
	if err != nil {
		return AWSIdentity{}, errors.New("resolve AWS identity: " + awsSDKErrorMessage(err))
	}
	return identity, nil
}

// libraryCheckAWSStatus never reports "aws CLI not installed"; other statuses match.
func libraryCheckAWSStatus(_ core.Context, provider model.CloudProviderConfig) model.CloudProviderStatus {
	_, err := awsSDKCallerIdentity(context.Background(), provider.Profile)
	if err == nil {
		return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
	}
	status := config.CloudTokenStatusExpired
	if isAWSProfileNotConfiguredError(err) {
		status = config.CloudTokenStatusNotConfigured
	}
	return model.CloudProviderStatus{CloudProviderConfig: provider, Status: status, Message: awsSDKErrorMessage(err)}
}

// libraryRunAWSBearerToken traces the same CLI line for parity, then uses aws-sdk-go-v2.
func libraryRunAWSBearerToken(ctx core.Context, profile, audience string) (string, error) {
	profile = strings.TrimSpace(profile)
	audience = normalizeCloudBearerAudience(audience)
	traceAWSGetWebIdentityToken(ctx, profile, audience)
	if ctx.DryRun {
		return "", nil
	}
	token, err := awsSDKWebIdentityToken(context.Background(), profile, audience)
	if err != nil {
		return "", fmt.Errorf("get AWS web identity token: %s", awsSDKErrorMessage(err))
	}
	return token, nil
}

// libraryRunAWSExportCredentials traces the same CLI line for parity, then uses aws-sdk-go-v2.
func libraryRunAWSExportCredentials(ctx core.Context, profile string) (CloudProviderCredentials, error) {
	profile = strings.TrimSpace(profile)
	traceAWSExportCredentials(ctx, profile)
	if ctx.DryRun {
		return CloudProviderCredentials{}, nil
	}
	creds, err := awsSDKExportCredentials(context.Background(), profile)
	if err != nil {
		return CloudProviderCredentials{}, fmt.Errorf("export AWS credentials: %s", awsSDKErrorMessage(err))
	}
	return creds, nil
}

// Shared by execution and trace so the dry-run line cannot drift.
func awsGetCallerIdentityArgs(profile string) []string {
	args := []string{"sts", "get-caller-identity", "--output", "json"}
	if profile = strings.TrimSpace(profile); profile != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

func traceAWSGetCallerIdentity(ctx core.Context, profile string) {
	ctx.TraceCommand("", "aws", awsGetCallerIdentityArgs(profile)...)
}

// Shared by execution and trace so the dry-run line cannot drift.
func awsGetWebIdentityTokenArgs(profile, audience string) []string {
	args := []string{
		"sts", "get-web-identity-token",
		"--audience", audience,
		"--signing-algorithm", "RS256",
		"--duration-seconds", "900",
		"--query", "WebIdentityToken",
		"--output", "text",
	}
	if profile = strings.TrimSpace(profile); profile != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

func traceAWSGetWebIdentityToken(ctx core.Context, profile, audience string) {
	ctx.TraceCommand("", "aws", awsGetWebIdentityTokenArgs(profile, audience)...)
}

// Shared by execution and trace so the dry-run line cannot drift.
func awsConfigureExportCredentialsArgs(profile string) []string {
	args := []string{"configure", "export-credentials", "--format", "process"}
	if profile = strings.TrimSpace(profile); profile != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

func traceAWSExportCredentials(ctx core.Context, profile string) {
	ctx.TraceCommand("", "aws", awsConfigureExportCredentialsArgs(profile)...)
}

func awsSDKCallerIdentity(ctx context.Context, profile string) (AWSIdentity, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if profile = strings.TrimSpace(profile); profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return AWSIdentity{}, err
	}
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return AWSIdentity{}, err
	}
	return AWSIdentity{
		Account: stringFromPtr(out.Account),
		Arn:     stringFromPtr(out.Arn),
		UserID:  stringFromPtr(out.UserId),
	}, nil
}

func awsSDKWebIdentityToken(ctx context.Context, profile, audience string) (string, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if profile = strings.TrimSpace(profile); profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return "", err
	}
	out, err := sts.NewFromConfig(cfg).GetWebIdentityToken(ctx, &sts.GetWebIdentityTokenInput{
		Audience:         []string{audience},
		SigningAlgorithm: awssdk.String("RS256"),
		DurationSeconds:  awssdk.Int32(900),
	})
	if err != nil {
		return "", err
	}
	return stringFromPtr(out.WebIdentityToken), nil
}

// awsSDKExportCredentials uses the SDK's credential chain, which reads the same ~/.aws files
// as the CLI.
func awsSDKExportCredentials(ctx context.Context, profile string) (CloudProviderCredentials, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if profile = strings.TrimSpace(profile); profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return CloudProviderCredentials{}, err
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return CloudProviderCredentials{}, err
	}
	result := CloudProviderCredentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
	}
	if creds.CanExpire {
		result.Expiration = creds.Expires
	}
	return result, nil
}

func stringFromPtr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func isAWSProfileNotConfiguredError(err error) bool {
	var notExist awsconfig.SharedConfigProfileNotExistError
	return errors.As(err, &notExist)
}

func awsSDKErrorMessage(err error) string {
	return strings.TrimSpace(err.Error())
}
