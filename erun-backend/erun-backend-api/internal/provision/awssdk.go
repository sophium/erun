package provision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	eruncommon "github.com/sophium/erun/erun-common"
)

// awsSDKRunner implements the eruncommon.CloudContextDependencies.RunAWS seam
// with aws-sdk-go-v2, so the API pod drives a tenant's AWS in-process and needs
// no `aws` executable — the image ships only the Go binary.
//
// eruncommon.InitCloudContext stays the single owner of the bootstrap sequence
// for every transport, so this stays on the far side of its existing seam and
// interprets the bounded argv that sequence emits. It is deliberately not a
// general aws-CLI parser: an argv it does not recognise is an error naming the
// argv, not a silent no-op, so a change on the eruncommon side surfaces here
// instead of half-provisioning a tenant.
type awsSDKRunner struct {
	creds awsCredentials
	// endpoint pins every service client at a local emulator (floci) for
	// verification; empty means the real AWS endpoints.
	endpoint string

	mu      sync.Mutex
	configs map[string]aws.Config
}

// awsCredentials is the tenant alias's stored credential blob.
type awsCredentials struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken,omitempty"`
}

// newAWSSDKRunner resolves the alias's credentials up front so a tenant whose
// alias carries no usable keys fails naming that, rather than provisioning
// against whatever ambient identity the control-plane pod happens to hold.
func newAWSSDKRunner(credentialsJSON, endpoint string) (*awsSDKRunner, error) {
	var creds awsCredentials
	if err := json.Unmarshal([]byte(credentialsJSON), &creds); err != nil {
		return nil, fmt.Errorf("stored credentials are not valid JSON: %w", err)
	}
	if strings.TrimSpace(creds.AccessKeyID) == "" || strings.TrimSpace(creds.SecretAccessKey) == "" {
		return nil, fmt.Errorf("stored credentials are missing accessKeyId or secretAccessKey")
	}
	return &awsSDKRunner{creds: creds, endpoint: endpoint, configs: map[string]aws.Config{}}, nil
}

// runAWS adapts the runner to the eruncommon seam, binding the DBOS step's
// context so a cancelled workflow cancels the in-flight AWS call.
func (r *awsSDKRunner) runAWS(ctx context.Context) func(eruncommon.Context, eruncommon.CloudProviderConfig, string, []string) (string, error) {
	return func(_ eruncommon.Context, _ eruncommon.CloudProviderConfig, region string, args []string) (string, error) {
		return r.run(ctx, region, args)
	}
}

func (r *awsSDKRunner) run(ctx context.Context, region string, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("aws: need a service and a verb, got %v", args)
	}
	cfg, err := r.config(ctx, region)
	if err != nil {
		return "", fmt.Errorf("aws config: %w", err)
	}
	service, verb, flags := args[0], args[1], parseAWSFlags(args[2:])
	switch service {
	case "ec2":
		return runEC2(ctx, ec2.NewFromConfig(cfg), verb, flags)
	case "iam":
		return runIAM(ctx, iam.NewFromConfig(cfg), verb, flags)
	case "ssm":
		return runSSM(ctx, ssm.NewFromConfig(cfg), verb, flags)
	default:
		return "", fmt.Errorf("aws: unsupported service %q in %v", service, args)
	}
}

// config caches one resolved config per region: the bootstrap makes a dozen
// calls in a single region, and resolving it once keeps them off the shared
// config files and IMDS the default chain would otherwise probe per call.
func (r *awsSDKRunner) config(ctx context.Context, region string) (aws.Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cfg, ok := r.configs[region]; ok {
		return cfg, nil
	}
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
		// The alias's keys are the only identity this may act as; withholding the
		// ambient chain is what keeps one tenant's provisioning off another
		// identity's credentials.
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			r.creds.AccessKeyID, r.creds.SecretAccessKey, r.creds.SessionToken)),
	}
	if r.endpoint != "" {
		opts = append(opts, awsconfig.WithBaseEndpoint(r.endpoint))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, err
	}
	r.configs[region] = cfg
	return cfg, nil
}

// ---- EC2 -------------------------------------------------------------------

func runEC2(ctx context.Context, client *ec2.Client, verb string, flags awsFlags) (string, error) {
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
	case "describe-security-groups":
		out, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
			GroupNames: flags.all("--group-names"),
		})
		if err != nil || len(out.SecurityGroups) == 0 {
			return "", err
		}
		return aws.ToString(out.SecurityGroups[0].GroupId), nil
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
	case "run-instances":
		return ec2RunInstances(ctx, client, flags)
	case "wait":
		return "", ec2Wait(ctx, client, flags)
	default:
		return "", fmt.Errorf("aws ec2: unsupported verb %q", verb)
	}
}

func ec2DescribeInstances(ctx context.Context, client *ec2.Client, flags awsFlags) (string, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: flags.all("--instance-ids"),
		Filters:     parseEC2Filters(flags.all("--filters")),
	})
	if err != nil {
		return "", err
	}
	// Render only the projections eruncommon asks for; anything else would be
	// parsed as a value it did not request.
	query := flags.one("--query")
	switch {
	case strings.Contains(query, "PublicIpAddress"):
		return firstInstanceField(out, func(i ec2types.Instance) string { return aws.ToString(i.PublicIpAddress) }), nil
	case strings.Contains(query, "[InstanceId,State.Name]"):
		return describeInstancesStateLines(out), nil
	case strings.Contains(query, "InstanceId"):
		return firstInstanceField(out, func(i ec2types.Instance) string { return aws.ToString(i.InstanceId) }), nil
	default:
		return "", fmt.Errorf("aws ec2 describe-instances: unsupported query %q", query)
	}
}

func ec2RunInstances(ctx context.Context, client *ec2.Client, flags awsFlags) (string, error) {
	count := int32(atoiOr(flags.one("--count"), 1))
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(flags.one("--image-id")),
		InstanceType: ec2types.InstanceType(flags.one("--instance-type")),
		MinCount:     aws.Int32(count),
		MaxCount:     aws.Int32(count),
	}
	// The CLI base64-encodes a file:// user-data argument; RunInstances expects
	// it already encoded.
	userData, err := readFileArg(flags.one("--user-data"))
	if err != nil {
		return "", err
	}
	if userData != "" {
		input.UserData = aws.String(base64.StdEncoding.EncodeToString([]byte(userData)))
	}
	if bdm, ok := parseBlockDeviceMapping(flags.one("--block-device-mappings")); ok {
		input.BlockDeviceMappings = []ec2types.BlockDeviceMapping{bdm}
	}
	if options, ok := parseMetadataOptions(flags.one("--metadata-options")); ok {
		input.MetadataOptions = options
	}
	if tags, ok := parseTagSpecification(flags.one("--tag-specifications")); ok {
		input.TagSpecifications = []ec2types.TagSpecification{tags}
	}
	if groups := flags.all("--security-group-ids"); len(groups) > 0 {
		input.SecurityGroupIds = groups
	}
	if profile, ok := parseIAMInstanceProfile(flags.one("--iam-instance-profile")); ok {
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

// ec2WaitMaxDuration matches the aws CLI waiter's ceiling (40 polls, 15s apart),
// so a bootstrap that hangs gives up at the same point it always did.
const ec2WaitMaxDuration = 10 * time.Minute

func ec2Wait(ctx context.Context, client *ec2.Client, flags awsFlags) error {
	input := &ec2.DescribeInstancesInput{InstanceIds: flags.all("--instance-ids")}
	switch state := flags.subcommand(); state {
	case "instance-running":
		return ec2.NewInstanceRunningWaiter(client).Wait(ctx, input, ec2WaitMaxDuration)
	case "instance-stopped":
		return ec2.NewInstanceStoppedWaiter(client).Wait(ctx, input, ec2WaitMaxDuration)
	default:
		return fmt.Errorf("aws ec2 wait: unsupported waiter %q", state)
	}
}

// ---- IAM -------------------------------------------------------------------

func runIAM(ctx context.Context, client *iam.Client, verb string, flags awsFlags) (string, error) {
	switch verb {
	case "get-role":
		out, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(flags.one("--role-name"))})
		if err != nil || out.Role == nil {
			return "", err
		}
		return aws.ToString(out.Role.RoleName), nil
	case "create-role":
		document, err := readFileArg(flags.one("--assume-role-policy-document"))
		if err != nil {
			return "", err
		}
		out, err := client.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 aws.String(flags.one("--role-name")),
			AssumeRolePolicyDocument: aws.String(document),
		})
		if err != nil || out.Role == nil {
			return "", err
		}
		return aws.ToString(out.Role.RoleName), nil
	case "put-role-policy":
		document, err := readFileArg(flags.one("--policy-document"))
		if err != nil {
			return "", err
		}
		_, err = client.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
			RoleName:       aws.String(flags.one("--role-name")),
			PolicyName:     aws.String(flags.one("--policy-name")),
			PolicyDocument: aws.String(document),
		})
		return "", err
	case "get-instance-profile":
		out, err := client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
			InstanceProfileName: aws.String(flags.one("--instance-profile-name")),
		})
		if err != nil || out.InstanceProfile == nil {
			return "", err
		}
		return instanceProfileField(out.InstanceProfile, flags.one("--query"))
	case "create-instance-profile":
		out, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
			InstanceProfileName: aws.String(flags.one("--instance-profile-name")),
		})
		if err != nil || out.InstanceProfile == nil {
			return "", err
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

// instanceProfileField renders the two projections eruncommon takes off an
// instance profile: its ARN, and the attached role's name — the idempotency
// check that decides whether a re-run must attach the role again.
func instanceProfileField(profile *iamtypes.InstanceProfile, query string) (string, error) {
	switch {
	case strings.Contains(query, "Roles[0].RoleName"):
		if len(profile.Roles) == 0 {
			return "", nil
		}
		return aws.ToString(profile.Roles[0].RoleName), nil
	case strings.Contains(query, "Arn"):
		return aws.ToString(profile.Arn), nil
	default:
		return "", fmt.Errorf("aws iam get-instance-profile: unsupported query %q", query)
	}
}

// ---- SSM -------------------------------------------------------------------

func runSSM(ctx context.Context, client *ssm.Client, verb string, flags awsFlags) (string, error) {
	if verb != "get-parameter" {
		return "", fmt.Errorf("aws ssm: unsupported verb %q", verb)
	}
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(flags.one("--name"))})
	if err != nil || out.Parameter == nil {
		return "", err
	}
	return aws.ToString(out.Parameter.Value), nil
}

// ---- argv parsing ----------------------------------------------------------

// awsFlags is one parsed argv tail: each flag maps to the values that followed
// it, and a leading bare token (the `wait instance-running` waiter name) is kept
// as the subcommand.
type awsFlags struct {
	values     map[string][]string
	positional []string
}

func parseAWSFlags(args []string) awsFlags {
	parsed := awsFlags{values: map[string][]string{}}
	current := ""
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--"):
			current = arg
			if _, ok := parsed.values[current]; !ok {
				parsed.values[current] = []string{}
			}
		case current == "":
			parsed.positional = append(parsed.positional, arg)
		default:
			parsed.values[current] = append(parsed.values[current], arg)
		}
	}
	return parsed
}

func (f awsFlags) one(name string) string {
	if values := f.values[name]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func (f awsFlags) all(name string) []string { return f.values[name] }

func (f awsFlags) subcommand() string {
	if len(f.positional) > 0 {
		return f.positional[0]
	}
	return ""
}

// parseEC2Filters turns each CLI shorthand "Name=k,Values=v1,v2" token into one
// EC2 filter.
func parseEC2Filters(tokens []string) []ec2types.Filter {
	var filters []ec2types.Filter
	for _, token := range tokens {
		name, values, ok := strings.Cut(strings.TrimPrefix(token, "Name="), ",Values=")
		if !ok || name == "" {
			continue
		}
		filters = append(filters, ec2types.Filter{Name: aws.String(name), Values: strings.Split(values, ",")})
	}
	return filters
}

var (
	deviceNamePattern  = regexp.MustCompile(`DeviceName=([^,]+)`)
	volumeSizePattern  = regexp.MustCompile(`VolumeSize=(\d+)`)
	volumeTypePattern  = regexp.MustCompile(`VolumeType=([a-z0-9]+)`)
	resourceTypePatten = regexp.MustCompile(`ResourceType=([a-z-]+)`)
	tagPattern         = regexp.MustCompile(`\{Key=([^,]+),Value=([^}]*)\}`)
)

// parseBlockDeviceMapping parses
// "DeviceName=/dev/sda1,Ebs={VolumeSize=N,VolumeType=T,DeleteOnTermination=true}".
func parseBlockDeviceMapping(value string) (ec2types.BlockDeviceMapping, bool) {
	if strings.TrimSpace(value) == "" {
		return ec2types.BlockDeviceMapping{}, false
	}
	mapping := ec2types.BlockDeviceMapping{Ebs: &ec2types.EbsBlockDevice{}}
	if m := deviceNamePattern.FindStringSubmatch(value); m != nil {
		mapping.DeviceName = aws.String(m[1])
	}
	if m := volumeSizePattern.FindStringSubmatch(value); m != nil {
		mapping.Ebs.VolumeSize = aws.Int32(int32(atoiOr(m[1], 0)))
	}
	if m := volumeTypePattern.FindStringSubmatch(value); m != nil {
		mapping.Ebs.VolumeType = ec2types.VolumeType(m[1])
	}
	if strings.Contains(value, "DeleteOnTermination=true") {
		mapping.Ebs.DeleteOnTermination = aws.Bool(true)
	}
	return mapping, true
}

// parseMetadataOptions parses
// "HttpEndpoint=enabled,HttpTokens=required,HttpPutResponseHopLimit=2".
func parseMetadataOptions(value string) (*ec2types.InstanceMetadataOptionsRequest, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, false
	}
	options := &ec2types.InstanceMetadataOptionsRequest{}
	for _, part := range strings.Split(value, ",") {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch key {
		case "HttpEndpoint":
			options.HttpEndpoint = ec2types.InstanceMetadataEndpointState(val)
		case "HttpTokens":
			options.HttpTokens = ec2types.HttpTokensState(val)
		case "HttpPutResponseHopLimit":
			options.HttpPutResponseHopLimit = aws.Int32(int32(atoiOr(val, 0)))
		}
	}
	return options, true
}

// parseTagSpecification parses
// "ResourceType=instance,Tags=[{Key=Name,Value=X},{Key=erun:context,Value=X}]".
func parseTagSpecification(value string) (ec2types.TagSpecification, bool) {
	if strings.TrimSpace(value) == "" {
		return ec2types.TagSpecification{}, false
	}
	spec := ec2types.TagSpecification{}
	if m := resourceTypePatten.FindStringSubmatch(value); m != nil {
		spec.ResourceType = ec2types.ResourceType(m[1])
	}
	for _, m := range tagPattern.FindAllStringSubmatch(value, -1) {
		spec.Tags = append(spec.Tags, ec2types.Tag{Key: aws.String(m[1]), Value: aws.String(m[2])})
	}
	return spec, true
}

// parseIAMInstanceProfile parses "Name=X" or "Arn=X".
func parseIAMInstanceProfile(value string) (*ec2types.IamInstanceProfileSpecification, bool) {
	key, val, ok := strings.Cut(strings.TrimSpace(value), "=")
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

// ---- rendering helpers -----------------------------------------------------

func firstInstanceField(out *ec2.DescribeInstancesOutput, field func(ec2types.Instance) string) string {
	for _, reservation := range out.Reservations {
		if len(reservation.Instances) > 0 {
			return field(reservation.Instances[0])
		}
	}
	return ""
}

// describeInstancesStateLines renders
// "Reservations[*].Instances[*].[InstanceId,State.Name]" as the CLI's
// tab-separated text, one instance per line.
func describeInstancesStateLines(out *ec2.DescribeInstancesOutput) string {
	var lines []string
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			state := ""
			if instance.State != nil {
				state = string(instance.State.Name)
			}
			lines = append(lines, aws.ToString(instance.InstanceId)+"\t"+state)
		}
	}
	return strings.Join(lines, "\n")
}

// readFileArg reads a "file://<path>" argument's contents as the aws CLI does;
// any other value passes through unchanged.
func readFileArg(value string) (string, error) {
	value = strings.TrimSpace(value)
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

func atoiOr(value string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		return n
	}
	return fallback
}
