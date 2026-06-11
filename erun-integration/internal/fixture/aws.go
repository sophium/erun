package fixture

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
)

// AWSStubError injects a deterministic AWS CLI failure for one argv family
// of the StubAWSCloudContext stub. Stderr is what the real AWS CLI would
// print (the classifiers in erun-common/cloud_context.go substring-match
// against it); ExitCode defaults to 254, the AWS CLI's client-error code.
type AWSStubError struct {
	Stderr   string
	ExitCode int
}

// AWSCloudContextStubSpec configures the argv-branching `aws` stub used by
// the real-run `erun context ...` scenarios. The success-output fields feed
// the decision inputs production code parses from AWS CLI stdout; the
// *Error fields flip one argv family to a canned failure so the error
// classifiers and recovery branches in erun-common/cloud_context.go become
// reachable. Zero values fall back to the defaults applied in
// StubAWSCloudContext, chosen so the happy-path flows complete.
type AWSCloudContextStubSpec struct {
	// RoleName is printed by `iam get-role` / `iam create-role`
	// (production ignores the value; only the exit code matters).
	RoleName string
	// InstanceProfileARN answers both `iam get-instance-profile
	// --query InstanceProfile.Arn` and `iam create-instance-profile`.
	InstanceProfileARN string
	// ProfileRoleName answers `iam get-instance-profile --query
	// InstanceProfile.Roles[0].RoleName`. "None" (the AWS text-output
	// null) means the profile carries no role yet.
	ProfileRoleName string
	// ActiveAssociationID / ActiveAssociationARN answer the
	// describe-iam-instance-profile-associations queries filtered on
	// state=associated. "None" means no active association.
	ActiveAssociationID  string
	ActiveAssociationARN string
	// PendingAssociationID answers the same query filtered on
	// state=associating,disassociating.
	PendingAssociationID string
	// PublicIP answers `ec2 describe-instances --query
	// Reservations[0].Instances[0].PublicIpAddress`.
	PublicIP string
	// SecurityGroupID answers `ec2 create-security-group` and
	// `ec2 describe-security-groups`.
	SecurityGroupID string
	// ImageID answers the `ssm get-parameter` AMI lookup.
	ImageID string
	// InstanceID answers `ec2 run-instances`.
	InstanceID string

	// Per-argv-family error injection. Nil means the call succeeds.
	GetRoleError                  *AWSStubError
	GetInstanceProfileError       *AWSStubError
	AddRoleToInstanceProfileError *AWSStubError
	AssociateInstanceProfileError *AWSStubError
	CreateSecurityGroupError      *AWSStubError
	AuthorizeIngressError         *AWSStubError
	StartInstancesError           *AWSStubError
	StopInstancesError            *AWSStubError
}

// StubAWSCloudContext writes an argv-branching `aws` stub at
// <stubsDir>/aws covering every AWS CLI call the cloud-context
// start/stop/init flows issue (see erun-common/cloud_context.go:
// ensureCloudContextInstanceProfile, ensureCloudContextInstanceProfile-
// Association, createCloudContextSecurityGroup, changeCloudContextPower-
// State, finalizeInitCloudContext). The returned env-var slice routes
// production `aws` invocations through the stub via ERUN_AWS_BIN.
//
// Arm order matters: the `Roles[0].RoleName` query must match before the
// generic `iam get-instance-profile` arm, and the association queries are
// distinguished by their state filter + --query selector so the three
// describe-iam-instance-profile-associations callers each get their own
// answer.
func StubAWSCloudContext(t testing.TB, stubsDir string, spec AWSCloudContextStubSpec) []string {
	t.Helper()
	spec = withAWSCloudContextDefaults(spec)
	arms := []string{
		awsCloudContextStubArm(`*"iam get-role"*`, spec.GetRoleError, spec.RoleName),
		awsCloudContextStubArm(`*"iam create-role"*`, nil, spec.RoleName),
		awsCloudContextStubArm(`*"iam put-role-policy"*`, nil, ""),
		awsCloudContextStubArm(`*"InstanceProfile.Roles[0].RoleName"*`, nil, spec.ProfileRoleName),
		awsCloudContextStubArm(`*"iam get-instance-profile"*`, spec.GetInstanceProfileError, spec.InstanceProfileARN),
		awsCloudContextStubArm(`*"iam create-instance-profile"*`, nil, spec.InstanceProfileARN),
		awsCloudContextStubArm(`*"iam add-role-to-instance-profile"*`, spec.AddRoleToInstanceProfileError, ""),
		awsCloudContextStubArm(`*"Name=state,Values=associated"*"AssociationId"*`, nil, spec.ActiveAssociationID),
		awsCloudContextStubArm(`*"Name=state,Values=associated"*"IamInstanceProfile.Arn"*`, nil, spec.ActiveAssociationARN),
		awsCloudContextStubArm(`*"Name=state,Values=associating,disassociating"*`, nil, spec.PendingAssociationID),
		awsCloudContextStubArm(`*"ec2 associate-iam-instance-profile"*`, spec.AssociateInstanceProfileError, ""),
		awsCloudContextStubArm(`*"ec2 create-security-group"*`, spec.CreateSecurityGroupError, spec.SecurityGroupID),
		awsCloudContextStubArm(`*"ec2 describe-security-groups"*`, nil, spec.SecurityGroupID),
		awsCloudContextStubArm(`*"ec2 authorize-security-group-ingress"*`, spec.AuthorizeIngressError, ""),
		awsCloudContextStubArm(`*"ssm get-parameter"*`, nil, spec.ImageID),
		awsCloudContextStubArm(`*"ec2 run-instances"*`, nil, spec.InstanceID),
		awsCloudContextStubArm(`*"ec2 start-instances"*`, spec.StartInstancesError, ""),
		awsCloudContextStubArm(`*"ec2 stop-instances"*`, spec.StopInstancesError, ""),
		awsCloudContextStubArm(`*"ec2 wait "*`, nil, ""),
		awsCloudContextStubArm(`*"PublicIpAddress"*`, nil, spec.PublicIP),
	}
	script := `case "$*" in` + "\n" +
		strings.Join(arms, "") +
		"  *) : ;;\n" +
		"esac\n" +
		"exit 0"
	StubBinaryWithScript(t, stubsDir, "aws", script)
	return StubEnv(stubsDir, "aws")
}

func withAWSCloudContextDefaults(spec AWSCloudContextStubSpec) AWSCloudContextStubSpec {
	defaults := []struct {
		field *string
		value string
	}{
		{&spec.RoleName, "stub-role"},
		{&spec.InstanceProfileARN, "arn:aws:iam::123456789012:instance-profile/stub-profile"},
		{&spec.ProfileRoleName, "None"},
		{&spec.ActiveAssociationID, "None"},
		{&spec.ActiveAssociationARN, "None"},
		{&spec.PendingAssociationID, "None"},
		{&spec.PublicIP, "203.0.113.10"},
		{&spec.SecurityGroupID, "sg-0123456789abcdef0"},
		{&spec.ImageID, "ami-0aabbccddeeff0011"},
		{&spec.InstanceID, "i-0fedcba9876543210"},
	}
	for _, d := range defaults {
		if *d.field == "" {
			*d.field = d.value
		}
	}
	return spec
}

// awsCloudContextStubArm renders one `case` arm: an injected failure prints
// its stderr and exits non-zero, a success prints the canned stdout (when
// any) and exits 0.
func awsCloudContextStubArm(pattern string, failure *AWSStubError, stdout string) string {
	if failure != nil {
		code := failure.ExitCode
		if code == 0 {
			code = 254
		}
		return "  " + pattern + ")\n" +
			"    printf '%s\\n' " + shellSingleQuote(failure.Stderr) + " >&2\n" +
			"    exit " + strconv.Itoa(code) + " ;;\n"
	}
	if stdout == "" {
		return "  " + pattern + ") exit 0 ;;\n"
	}
	return "  " + pattern + ")\n" +
		"    printf '%s\\n' " + shellSingleQuote(stdout) + "\n" +
		"    exit 0 ;;\n"
}

// SeedAWSSharedConfig writes <home>/.aws/config with one SSO profile for
// the given account, using the modern sso_session indirection so the
// parser's session-index path (erun-common/aws_sso_config.go) is exercised:
// the profile carries sso_account_id + sso_role_name + region and points
// at an [sso-session] block that owns sso_start_url + sso_region. Returns
// the SSO start URL it wrote so tests can assert it flowed through.
func SeedAWSSharedConfig(t testing.TB, setup env.Setup, accountID, profileName string) string {
	t.Helper()
	startURL := "https://corp.awsapps.com/start"
	dir := filepath.Join(setup.Home, ".aws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := "# AWS shared config seeded by the erun integration suite.\n" +
		"[default]\n" +
		"region = us-east-1\n" +
		"\n" +
		"[profile " + profileName + "]\n" +
		"sso_session = corp\n" +
		"sso_account_id = " + accountID + "\n" +
		"sso_role_name = AdminRole\n" +
		"region = eu-west-2\n" +
		"\n" +
		"[sso-session corp]\n" +
		"sso_start_url = " + startURL + "\n" +
		"sso_region = eu-west-1\n" +
		"sso_registration_scopes = sso:account:access\n"
	mustWrite(t, filepath.Join(dir, "config"), body)
	return startURL
}
