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
// Once makes the failure fire only on the family's first invocation — the
// stub drops a marker file in the stubs dir and answers success afterwards —
// so retry/recovery loops in production can be driven to their success arm.
type AWSStubError struct {
	Stderr   string
	ExitCode int
	Once     bool
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
	// InstanceStates answers the bulk status-refresh query
	// (`ec2 describe-instances --query
	// Reservations[*].Instances[*].[InstanceId,State.Name] --output text`)
	// issued by RefreshCloudContextStatuses. Multi-line "id<TAB>state"
	// text, exactly what the AWS CLI prints. Empty answers the query with
	// no output (every instance reads as "not found in AWS").
	InstanceStates string
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
	RunInstancesError             *AWSStubError
	WaitError                     *AWSStubError
	DescribeInstanceStatesError   *AWSStubError
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
	arm := func(pattern string, failure *AWSStubError, stdout string) string {
		return awsCloudContextStubArm(stubsDir, pattern, failure, stdout)
	}
	arms := []string{
		arm(`*"iam get-role"*`, spec.GetRoleError, spec.RoleName),
		arm(`*"iam create-role"*`, nil, spec.RoleName),
		arm(`*"iam put-role-policy"*`, nil, ""),
		arm(`*"InstanceProfile.Roles[0].RoleName"*`, nil, spec.ProfileRoleName),
		arm(`*"iam get-instance-profile"*`, spec.GetInstanceProfileError, spec.InstanceProfileARN),
		arm(`*"iam create-instance-profile"*`, nil, spec.InstanceProfileARN),
		arm(`*"iam add-role-to-instance-profile"*`, spec.AddRoleToInstanceProfileError, ""),
		arm(`*"Name=state,Values=associated"*"AssociationId"*`, nil, spec.ActiveAssociationID),
		arm(`*"Name=state,Values=associated"*"IamInstanceProfile.Arn"*`, nil, spec.ActiveAssociationARN),
		arm(`*"Name=state,Values=associating,disassociating"*`, nil, spec.PendingAssociationID),
		arm(`*"ec2 associate-iam-instance-profile"*`, spec.AssociateInstanceProfileError, ""),
		arm(`*"ec2 create-security-group"*`, spec.CreateSecurityGroupError, spec.SecurityGroupID),
		arm(`*"ec2 describe-security-groups"*`, nil, spec.SecurityGroupID),
		arm(`*"ec2 authorize-security-group-ingress"*`, spec.AuthorizeIngressError, ""),
		arm(`*"ssm get-parameter"*`, nil, spec.ImageID),
		arm(`*"ec2 run-instances"*`, spec.RunInstancesError, spec.InstanceID),
		arm(`*"ec2 start-instances"*`, spec.StartInstancesError, ""),
		arm(`*"ec2 stop-instances"*`, spec.StopInstancesError, ""),
		arm(`*"ec2 wait "*`, spec.WaitError, ""),
		arm(`*"[InstanceId,State.Name]"*`, spec.DescribeInstanceStatesError, spec.InstanceStates),
		arm(`*"PublicIpAddress"*`, nil, spec.PublicIP),
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
// any) and exits 0. A failure with Once=true fires only on the family's
// first invocation: the arm drops a marker file derived from the pattern
// into stubsDir and answers the success response on every later call, so
// production retry loops can be driven through failure and into recovery.
func awsCloudContextStubArm(stubsDir, pattern string, failure *AWSStubError, stdout string) string {
	successLines := []string{"exit 0 ;;"}
	if stdout != "" {
		successLines = []string{"printf '%s\\n' " + shellSingleQuote(stdout), "exit 0 ;;"}
	}
	lines := []string{pattern + ")"}
	if failure != nil {
		code := failure.ExitCode
		if code == 0 {
			code = 254
		}
		failLines := []string{
			"printf '%s\\n' " + shellSingleQuote(failure.Stderr) + " >&2",
			"exit " + strconv.Itoa(code),
		}
		if failure.Once {
			marker := shellSingleQuote(filepath.Join(stubsDir, "aws-once-"+sanitizeFilename(pattern)))
			lines = append(lines, "  if [ ! -f "+marker+" ]; then", "    : > "+marker)
			for _, l := range failLines {
				lines = append(lines, "    "+l)
			}
			lines = append(lines, "  fi")
		} else {
			failLines[len(failLines)-1] += " ;;"
			for _, l := range failLines {
				lines = append(lines, "  "+l)
			}
			return renderStubArmLines(lines)
		}
	}
	for _, l := range successLines {
		lines = append(lines, "  "+l)
	}
	return renderStubArmLines(lines)
}

// renderStubArmLines joins one arm's lines with the two-space base indent the
// surrounding `case "$*" in` body uses.
func renderStubArmLines(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
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
