package fixture

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
)

// AWSStubError injects a deterministic AWS CLI failure for one argv family.
// Stderr must read like the real AWS CLI's, because the classifiers in
// erun-common/cloud_context.go substring-match against it; ExitCode defaults
// to 254, the AWS CLI's client-error code. Once fires the failure only on the
// family's first invocation, so production retry/recovery loops can be driven
// to their success arm.
type AWSStubError struct {
	Stderr   string
	ExitCode int
	Once     bool
}

// AWSCloudContextStubSpec configures the argv-branching `aws` stub for the
// real-run `erun context ...` scenarios. Success-output fields feed the
// decision inputs production parses from AWS CLI stdout; the *Error fields
// make the error classifiers and recovery branches in
// erun-common/cloud_context.go reachable. Zero values fall back to defaults
// chosen so the happy path completes.
type AWSCloudContextStubSpec struct {
	// RoleName's value is ignored by production; only the call's exit code matters.
	RoleName string
	InstanceProfileARN string
	// ProfileRoleName of "None" (the AWS text-output null) means the profile
	// carries no role yet.
	ProfileRoleName string
	// ActiveAssociationID / ActiveAssociationARN of "None" mean no active
	// association.
	ActiveAssociationID  string
	ActiveAssociationARN string
	PendingAssociationID string
	PublicIP string
	// InstanceStates is multi-line "id<TAB>state" text as the AWS CLI prints
	// it; empty makes every instance read as "not found in AWS".
	InstanceStates string
	SecurityGroupID string
	ImageID string
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

// StubAWSCloudContext writes an argv-branching `aws` stub covering every AWS
// CLI call the cloud-context start/stop/init flows issue, and returns the env
// vars that route production `aws` invocations through it.
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

func renderStubArmLines(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// SeedAWSSharedConfig writes <home>/.aws/config with one SSO profile, using
// the modern sso_session indirection so the parser's session-index path
// (erun-common/aws_sso_config.go) is exercised. Returns the SSO start URL it
// wrote so tests can assert it flowed through.
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
