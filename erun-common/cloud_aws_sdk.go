package eruncommon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// awsSTSIdentityExecutionOperation is the ExecutionModeFor/ExecutionModeReport
// key for the `aws sts get-caller-identity` call sites (defaultResolveAWSIdentity
// and defaultCheckAWSStatus below). Every remaining AWS operation stays
// subprocess-only for its own reason: `aws sso login`/`aws sso logout` drive a
// real browser device-code flow; `aws configure set` writes the shared
// ~/.aws/config ini file rather than calling an AWS API; and the erun-oidc
// federation setup (`aws iam enable-outbound-web-identity-federation`) is a
// one-shot, rare call that would need a new IAM SDK dependency for a single
// call site. `aws configure export-credentials` had been lumped in with these,
// but it does neither — it only resolves the profile's credential chain, which
// aws-sdk-go-v2's config package already replicates — so it is promoted below
// as aws-export-credentials instead.
const awsSTSIdentityExecutionOperation = "aws-sts"

// awsSTSWebIdentityTokenExecutionOperation is the ExecutionModeFor/
// ExecutionModeReport key for the `aws sts get-web-identity-token` call site
// (defaultRunAWSBearerToken below), the OIDC bearer-token mint
// awsCloudProviderBearerToken uses to federate an AWS-provider alias with
// erun. Kept distinct from awsSTSIdentityExecutionOperation per
// execution_mode.go's convention of one key per ported call site, even though
// both hang off the same aws-sdk-go-v2 STS client.
const awsSTSWebIdentityTokenExecutionOperation = "aws-sts-web-identity-token"

// awsExportCredentialsExecutionOperation is the ExecutionModeFor/
// ExecutionModeReport key for the `aws configure export-credentials` call site
// (defaultRunAWSExportCredentials below), the credential mint
// ExportCloudProviderCredentials uses to seed a remote runtime pod with the
// operator's host AWS identity — driven by `erun cloud refresh` and by the
// desktop's background credential refresher (erun-ui/cloud_credentials_refresher.go),
// which calls it roughly every 45 minutes for as long as an AWS-aliased
// environment stays open.
const awsExportCredentialsExecutionOperation = "aws-export-credentials"

// libraryResolveAWSIdentity is the library-backed alternative to
// defaultResolveAWSIdentity. It renders the exact same `aws sts
// get-caller-identity` trace line for dry-run/audit parity, then resolves the
// identity via aws-sdk-go-v2 instead of shelling out to the aws CLI.
func libraryResolveAWSIdentity(ctx Context, profile string) (AWSIdentity, error) {
	profile = strings.TrimSpace(profile)
	traceAWSGetCallerIdentity(ctx, profile)
	identity, err := awsSDKCallerIdentity(context.Background(), profile)
	if err != nil {
		return AWSIdentity{}, errors.New("resolve AWS identity: " + awsSDKErrorMessage(err))
	}
	return identity, nil
}

// libraryCheckAWSStatus is the library-backed alternative to
// defaultCheckAWSStatus. Unlike the subprocess version it can never observe
// "aws CLI is not installed" — there is no CLI to be missing — so that status
// arm never fires here; every other classification (active, not configured,
// expired) is preserved.
func libraryCheckAWSStatus(_ Context, provider CloudProviderConfig) CloudProviderStatus {
	_, err := awsSDKCallerIdentity(context.Background(), provider.Profile)
	if err == nil {
		return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}
	}
	status := CloudTokenStatusExpired
	if isAWSProfileNotConfiguredError(err) {
		status = CloudTokenStatusNotConfigured
	}
	return CloudProviderStatus{CloudProviderConfig: provider, Status: status, Message: awsSDKErrorMessage(err)}
}

// libraryRunAWSBearerToken is the library-backed alternative to
// defaultRunAWSBearerToken. It renders the exact same `aws sts
// get-web-identity-token` trace line for dry-run/audit parity — including the
// unconditional dry-run trace awsBearerTokenWithOIDCRetry issues a second time
// to preview its federation-enable recovery — then mints the token via
// aws-sdk-go-v2 instead of shelling out to the aws CLI.
func libraryRunAWSBearerToken(ctx Context, profile, audience string) (string, error) {
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

// libraryRunAWSExportCredentials is the library-backed alternative to
// defaultRunAWSExportCredentials. It renders the exact same `aws configure
// export-credentials` trace line for dry-run/audit parity, then resolves the
// profile's credentials via aws-sdk-go-v2's own credential provider chain
// (static keys, SSO, assume-role, credential_process, IMDS — the same
// ~/.aws/config and ~/.aws/sso cache the aws CLI itself reads) instead of
// shelling out to the aws CLI.
func libraryRunAWSExportCredentials(ctx Context, profile string) (CloudProviderCredentials, error) {
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

// awsGetCallerIdentityArgs is the single source of the `aws sts
// get-caller-identity` argv, shared by defaultResolveAWSIdentity (which also
// executes it as a subprocess) and traceAWSGetCallerIdentity (which only
// renders it), so the dry-run/audit trace can never drift from either
// execution path.
func awsGetCallerIdentityArgs(profile string) []string {
	args := []string{"sts", "get-caller-identity", "--output", "json"}
	if profile = strings.TrimSpace(profile); profile != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

func traceAWSGetCallerIdentity(ctx Context, profile string) {
	ctx.TraceCommand("", "aws", awsGetCallerIdentityArgs(profile)...)
}

// awsGetWebIdentityTokenArgs is the single source of the `aws sts
// get-web-identity-token` argv, shared by defaultRunAWSBearerToken (which also
// executes it as a subprocess) and traceAWSGetWebIdentityToken (which only
// renders it), so the dry-run/audit trace can never drift from either
// execution path.
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

func traceAWSGetWebIdentityToken(ctx Context, profile, audience string) {
	ctx.TraceCommand("", "aws", awsGetWebIdentityTokenArgs(profile, audience)...)
}

// awsConfigureExportCredentialsArgs is the single source of the `aws configure
// export-credentials` argv, shared by defaultRunAWSExportCredentials (which
// also executes it as a subprocess) and traceAWSExportCredentials (which only
// renders it), so the dry-run/audit trace can never drift from either
// execution path.
func awsConfigureExportCredentialsArgs(profile string) []string {
	args := []string{"configure", "export-credentials", "--format", "process"}
	if profile = strings.TrimSpace(profile); profile != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

func traceAWSExportCredentials(ctx Context, profile string) {
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

// awsSDKExportCredentials resolves profile's credentials through
// aws-sdk-go-v2's own provider chain instead of the aws CLI's botocore
// equivalent. Both read the same shared ~/.aws/config, ~/.aws/credentials, and
// ~/.aws/sso/cache files and implement the same publicly documented AWS
// credential chain, so no new network call is needed for the common
// static-key, SSO, or assume-role cases — only whichever of those the chain
// itself would already contact (STS for assume-role, SSO OIDC for a cached SSO
// token) does one.
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
