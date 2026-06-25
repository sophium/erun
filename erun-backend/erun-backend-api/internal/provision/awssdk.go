package provision

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	eruncommon "github.com/sophium/erun/erun-common"
)

// awsSDKRunner implements the eruncommon.CloudContextDependencies.RunAWS seam
// with aws-sdk-go-v2 instead of shelling the `aws` CLI, so the provisioning DBOS
// workflow drives AWS purely through the SDK (issue #682) — the API pod runs no
// `aws` executable. It interprets the bounded argv that eruncommon.InitCloudContext
// emits (it never needs to be a general aws-CLI parser; both sides are this repo)
// and renders results in the `--query --output text` shape InitCloudContext
// re-parses. eruncommon.InitCloudContext and the CLI/desktop transports are
// unchanged — only this backend RunAWS implementation differs.
type awsSDKRunner struct {
	creds awsCredentials
	// endpoint pins every service client at a local emulator (floci) for
	// verification; empty means the real AWS endpoints.
	endpoint string
}

// run is the eruncommon RunAWS closure. ctx carries cancellation from the DBOS
// step. provider is unused (credentials are captured); region + args come from
// InitCloudContext.
func (r awsSDKRunner) run(ctx context.Context, _ eruncommon.CloudProviderConfig, region string, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("aws: need service and verb, got %v", args)
	}
	cfg, err := r.loadConfig(ctx, region)
	if err != nil {
		return "", fmt.Errorf("aws config: %w", err)
	}
	service, verb, rest := args[0], args[1], args[2:]
	flags := parseAWSFlags(rest)
	switch service {
	case "ec2":
		return r.ec2(ctx, cfg, verb, flags)
	case "iam":
		return r.iam(ctx, cfg, verb, flags)
	case "ssm":
		return r.ssm(ctx, cfg, verb, flags)
	default:
		return "", fmt.Errorf("aws: unsupported service %q", service)
	}
}

func (r awsSDKRunner) loadConfig(ctx context.Context, region string) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			r.creds.AccessKeyID, r.creds.SecretAccessKey, r.creds.SessionToken)),
	}
	if r.endpoint != "" {
		opts = append(opts, awsconfig.WithBaseEndpoint(r.endpoint))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

// ---- EC2 -------------------------------------------------------------------

func (r awsSDKRunner) ec2(ctx context.Context, cfg aws.Config, verb string, flags awsFlags) (string, error) {
	client := ec2.NewFromConfig(cfg)
	switch verb {
	case "describe-instances":
		return ec2DescribeInstances(ctx, client, flags)
	case "create-security-group":
		out, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
			GroupName:   aws.String(flags.one("--group-name")),
			Description: aws.String(flags.one("--description")),
		})
		if err != nil {
			return "", err
		}
		return aws.ToString(out.GroupId), nil
	case "authorize-security-group-ingress":
		port := int32(atoiOr(flags.one("--port"), 0))
		_, err := client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId: aws.String(flags.one("--group-id")),
			IpPermissions: []ec2types.IpPermission{{
				IpProtocol: aws.String(flags.one("--protocol")),
				FromPort:   aws.Int32(port),
				ToPort:     aws.Int32(port),
				IpRanges:   []ec2types.IpRange{{CidrIp: aws.String(flags.one("--cidr"))}},
			}},
		})
		return "", err
	case "describe-security-groups":
		out, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
			GroupNames: flags.all("--group-names"),
		})
		if err != nil || len(out.SecurityGroups) == 0 {
			return "", err
		}
		return aws.ToString(out.SecurityGroups[0].GroupId), nil
	case "run-instances":
		return ec2RunInstances(ctx, client, flags)
	case "wait":
		return "", ec2Wait(ctx, client, flags)
	default:
		return "", fmt.Errorf("aws ec2: unsupported verb %q", verb)
	}
}

func ec2DescribeInstances(ctx context.Context, client *ec2.Client, flags awsFlags) (string, error) {
	input := &ec2.DescribeInstancesInput{InstanceIds: flags.all("--instance-ids")}
	if filters := parseEC2Filters(flags.all("--filters")); len(filters) > 0 {
		input.Filters = filters
	}
	out, err := client.DescribeInstances(ctx, input)
	if err != nil {
		return "", err
	}
	query := flags.one("--query")
	switch {
	case strings.Contains(query, "PublicIpAddress"):
		if inst, ok := firstInstance(out); ok {
			return aws.ToString(inst.PublicIpAddress), nil
		}
	case strings.Contains(query, "[InstanceId,State.Name]"):
		return describeInstancesStateLines(out), nil
	default: // InstanceId
		if inst, ok := firstInstance(out); ok {
			return aws.ToString(inst.InstanceId), nil
		}
	}
	return "", nil
}

func ec2RunInstances(ctx context.Context, client *ec2.Client, flags awsFlags) (string, error) {
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(flags.one("--image-id")),
		InstanceType: ec2types.InstanceType(flags.one("--instance-type")),
		MinCount:     aws.Int32(int32(atoiOr(flags.one("--count"), 1))),
		MaxCount:     aws.Int32(int32(atoiOr(flags.one("--count"), 1))),
	}
	if userData, err := readFileArg(flags.one("--user-data")); err != nil {
		return "", err
	} else if userData != "" {
		input.UserData = aws.String(base64.StdEncoding.EncodeToString([]byte(userData)))
	}
	if bdm, ok := parseBlockDeviceMapping(flags.one("--block-device-mappings")); ok {
		input.BlockDeviceMappings = []ec2types.BlockDeviceMapping{bdm}
	}
	if mo, ok := parseMetadataOptions(flags.one("--metadata-options")); ok {
		input.MetadataOptions = mo
	}
	if ts, ok := parseTagSpecification(flags.one("--tag-specifications")); ok {
		input.TagSpecifications = []ec2types.TagSpecification{ts}
	}
	if sg := flags.all("--security-group-ids"); len(sg) > 0 {
		input.SecurityGroupIds = sg
	}
	if profile, ok := parseIamInstanceProfile(flags.one("--iam-instance-profile")); ok {
		input.IamInstanceProfile = profile
	}
	if subnet := flags.one("--subnet-id"); subnet != "" {
		input.SubnetId = aws.String(subnet)
	}
	if key := flags.one("--key-name"); key != "" {
		input.KeyName = aws.String(key)
	}
	out, err := client.RunInstances(ctx, input)
	if err != nil {
		return "", err
	}
	if len(out.Instances) == 0 {
		return "", nil
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

func ec2Wait(ctx context.Context, client *ec2.Client, flags awsFlags) error {
	ids := flags.all("--instance-ids")
	input := &ec2.DescribeInstancesInput{InstanceIds: ids}
	const maxWait = 10 * time.Minute
	if len(flags.all("--instance-ids")) > 0 && flags.one2() == "instance-stopped" {
		return ec2.NewInstanceStoppedWaiter(client).Wait(ctx, input, maxWait)
	}
	return ec2.NewInstanceRunningWaiter(client).Wait(ctx, input, maxWait)
}

// ---- IAM -------------------------------------------------------------------

func (r awsSDKRunner) iam(ctx context.Context, cfg aws.Config, verb string, flags awsFlags) (string, error) {
	client := iam.NewFromConfig(cfg)
	switch verb {
	case "get-role":
		out, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(flags.one("--role-name"))})
		if err != nil {
			return "", err
		}
		if out.Role == nil {
			return "", nil
		}
		return aws.ToString(out.Role.RoleName), nil
	case "create-role":
		doc, err := readFileArg(flags.one("--assume-role-policy-document"))
		if err != nil {
			return "", err
		}
		out, err := client.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 aws.String(flags.one("--role-name")),
			AssumeRolePolicyDocument: aws.String(doc),
		})
		if err != nil {
			return "", err
		}
		if out.Role == nil {
			return "", nil
		}
		return aws.ToString(out.Role.RoleName), nil
	case "put-role-policy":
		doc, err := readFileArg(flags.one("--policy-document"))
		if err != nil {
			return "", err
		}
		_, err = client.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
			RoleName:       aws.String(flags.one("--role-name")),
			PolicyName:     aws.String(flags.one("--policy-name")),
			PolicyDocument: aws.String(doc),
		})
		return "", err
	case "get-instance-profile":
		out, err := client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
			InstanceProfileName: aws.String(flags.one("--instance-profile-name")),
		})
		if err != nil {
			return "", err
		}
		if out.InstanceProfile == nil {
			return "", nil
		}
		// get-instance-profile is queried two ways: for the profile ARN, and for
		// the attached role's name (the idempotency check on a re-run).
		if strings.Contains(flags.one("--query"), "Roles[0].RoleName") {
			if len(out.InstanceProfile.Roles) > 0 {
				return aws.ToString(out.InstanceProfile.Roles[0].RoleName), nil
			}
			return "", nil
		}
		return aws.ToString(out.InstanceProfile.Arn), nil
	case "create-instance-profile":
		out, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
			InstanceProfileName: aws.String(flags.one("--instance-profile-name")),
		})
		if err != nil {
			return "", err
		}
		if out.InstanceProfile == nil {
			return "", nil
		}
		return aws.ToString(out.InstanceProfile.Arn), nil
	case "add-role-to-instance-profile":
		_, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
			InstanceProfileName: aws.String(flags.one("--instance-profile-name")),
			RoleName:            aws.String(flags.one("--role-name")),
		})
		return "", err
	default:
		return "", fmt.Errorf("aws iam: unsupported verb %q", verb)
	}
}

// ---- SSM -------------------------------------------------------------------

func (r awsSDKRunner) ssm(ctx context.Context, cfg aws.Config, verb string, flags awsFlags) (string, error) {
	if verb != "get-parameter" {
		return "", fmt.Errorf("aws ssm: unsupported verb %q", verb)
	}
	client := ssm.NewFromConfig(cfg)
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(flags.one("--name"))})
	if err != nil {
		return "", err
	}
	if out.Parameter == nil {
		return "", nil
	}
	return aws.ToString(out.Parameter.Value), nil
}

// ---- argv + shorthand parsing ---------------------------------------------

// awsFlags is the parsed argv: each flag maps to the values that followed it
// (until the next "--flag"). A `wait <subcommand>` carries the subcommand as the
// first positional, tracked separately under the "" key.
type awsFlags struct {
	flags      map[string][]string
	positional []string
}

func (f awsFlags) one(name string) string {
	if v := f.flags[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func (f awsFlags) all(name string) []string { return f.flags[name] }

// one2 returns the first positional token (e.g. the `wait instance-running`
// subcommand that precedes the flags).
func (f awsFlags) one2() string {
	if len(f.positional) > 0 {
		return f.positional[0]
	}
	return ""
}

func parseAWSFlags(args []string) awsFlags {
	out := awsFlags{flags: map[string][]string{}}
	cur := ""
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			cur = a
			if _, ok := out.flags[cur]; !ok {
				out.flags[cur] = []string{}
			}
			continue
		}
		if cur == "" {
			out.positional = append(out.positional, a)
			continue
		}
		out.flags[cur] = append(out.flags[cur], a)
	}
	return out
}

// parseEC2Filters turns CLI shorthand "Name=k,Values=v1,v2" tokens into EC2
// filters. Each --filters value is one Name=...,Values=... token.
func parseEC2Filters(tokens []string) []ec2types.Filter {
	var filters []ec2types.Filter
	for _, tok := range tokens {
		name := ""
		var values []string
		for _, part := range splitTopLevel(tok) {
			key, val, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			switch key {
			case "Name":
				name = val
			case "Values":
				values = strings.Split(val, ",")
			}
		}
		if name != "" {
			filters = append(filters, ec2types.Filter{Name: aws.String(name), Values: values})
		}
	}
	return filters
}

// splitTopLevel splits on commas that are not inside [...] or {...}. The CLI
// shorthand nests Values=v1,v2 and Tags=[{...}], so a naive comma split breaks.
func splitTopLevel(s string) []string {
	var parts []string
	depth := 0
	last := 0
	for i, c := range s {
		switch c {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case ',':
			if depth == 0 {
				// Keep Values=v1,v2 together: only split before a Key= token.
				rest := s[i+1:]
				if strings.HasPrefix(rest, "Name=") || strings.HasPrefix(rest, "Values=") ||
					strings.HasPrefix(rest, "Ebs=") || strings.HasPrefix(rest, "DeviceName=") ||
					strings.HasPrefix(rest, "Tags=") || strings.HasPrefix(rest, "ResourceType=") {
					parts = append(parts, s[last:i])
					last = i + 1
				}
			}
		}
	}
	return append(parts, s[last:])
}

// parseBlockDeviceMapping parses
// "DeviceName=/dev/sda1,Ebs={VolumeSize=N,VolumeType=T,DeleteOnTermination=true}".
func parseBlockDeviceMapping(s string) (ec2types.BlockDeviceMapping, bool) {
	if strings.TrimSpace(s) == "" {
		return ec2types.BlockDeviceMapping{}, false
	}
	bdm := ec2types.BlockDeviceMapping{}
	if m := regexp.MustCompile(`DeviceName=([^,]+)`).FindStringSubmatch(s); m != nil {
		bdm.DeviceName = aws.String(m[1])
	}
	ebs := &ec2types.EbsBlockDevice{}
	if m := regexp.MustCompile(`VolumeSize=(\d+)`).FindStringSubmatch(s); m != nil {
		ebs.VolumeSize = aws.Int32(int32(atoiOr(m[1], 0)))
	}
	if m := regexp.MustCompile(`VolumeType=([a-z0-9]+)`).FindStringSubmatch(s); m != nil {
		ebs.VolumeType = ec2types.VolumeType(m[1])
	}
	if strings.Contains(s, "DeleteOnTermination=true") {
		ebs.DeleteOnTermination = aws.Bool(true)
	}
	bdm.Ebs = ebs
	return bdm, true
}

// parseMetadataOptions parses
// "HttpEndpoint=enabled,HttpTokens=required,HttpPutResponseHopLimit=2".
func parseMetadataOptions(s string) (*ec2types.InstanceMetadataOptionsRequest, bool) {
	if strings.TrimSpace(s) == "" {
		return nil, false
	}
	mo := &ec2types.InstanceMetadataOptionsRequest{}
	for _, part := range strings.Split(s, ",") {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch key {
		case "HttpEndpoint":
			mo.HttpEndpoint = ec2types.InstanceMetadataEndpointState(val)
		case "HttpTokens":
			mo.HttpTokens = ec2types.HttpTokensState(val)
		case "HttpPutResponseHopLimit":
			mo.HttpPutResponseHopLimit = aws.Int32(int32(atoiOr(val, 0)))
		}
	}
	return mo, true
}

// parseTagSpecification parses
// "ResourceType=instance,Tags=[{Key=Name,Value=X},{Key=erun:context,Value=X}]".
func parseTagSpecification(s string) (ec2types.TagSpecification, bool) {
	if strings.TrimSpace(s) == "" {
		return ec2types.TagSpecification{}, false
	}
	ts := ec2types.TagSpecification{}
	if m := regexp.MustCompile(`ResourceType=([a-z-]+)`).FindStringSubmatch(s); m != nil {
		ts.ResourceType = ec2types.ResourceType(m[1])
	}
	for _, m := range regexp.MustCompile(`\{Key=([^,]+),Value=([^}]*)\}`).FindAllStringSubmatch(s, -1) {
		ts.Tags = append(ts.Tags, ec2types.Tag{Key: aws.String(m[1]), Value: aws.String(m[2])})
	}
	return ts, true
}

// parseIamInstanceProfile parses "Name=X" or "Arn=X".
func parseIamInstanceProfile(s string) (*ec2types.IamInstanceProfileSpecification, bool) {
	key, val, ok := strings.Cut(strings.TrimSpace(s), "=")
	if !ok {
		return nil, false
	}
	switch key {
	case "Name":
		return &ec2types.IamInstanceProfileSpecification{Name: aws.String(val)}, true
	case "Arn":
		return &ec2types.IamInstanceProfileSpecification{Arn: aws.String(val)}, true
	}
	return nil, false
}

// ---- helpers ---------------------------------------------------------------

func firstInstance(out *ec2.DescribeInstancesOutput) (ec2types.Instance, bool) {
	for _, res := range out.Reservations {
		if len(res.Instances) > 0 {
			return res.Instances[0], true
		}
	}
	return ec2types.Instance{}, false
}

// describeInstancesStateLines renders "Reservations[*].Instances[*].
// [InstanceId,State.Name]" as the CLI's tab-separated text (one instance per
// line), the shape RefreshCloudContextStatuses parses.
func describeInstancesStateLines(out *ec2.DescribeInstancesOutput) string {
	var lines []string
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			state := ""
			if inst.State != nil {
				state = string(inst.State.Name)
			}
			lines = append(lines, aws.ToString(inst.InstanceId)+"\t"+state)
		}
	}
	return strings.Join(lines, "\n")
}

// readFileArg reads a "file://<path>" argument's contents, as the aws CLI does.
// A non-file value is returned verbatim; an empty value yields "".
func readFileArg(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	path, ok := strings.CutPrefix(value, "file://")
	if !ok {
		return value, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", value, err)
	}
	return string(data), nil
}

func atoiOr(s string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return fallback
}
