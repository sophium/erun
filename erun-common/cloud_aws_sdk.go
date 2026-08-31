package eruncommon

import (
	"context"
	"errors"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// awsSTSIdentityExecutionOperation is the ExecutionModeFor/ExecutionModeReport
// key for the `aws sts get-caller-identity` call sites (defaultResolveAWSIdentity
// and defaultCheckAWSStatus below), the two AWS operations promoted to a
// library path so far. Every other AWS operation (sso login/logout, configure
// set, configure export-credentials, the erun-oidc federation setup) stays
// subprocess-only: they either drive a real browser SSO flow or write the
// shared ~/.aws/config ini file, neither of which aws-sdk-go-v2 replaces.
const awsSTSIdentityExecutionOperation = "aws-sts"

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
