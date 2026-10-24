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
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// awsSDKRunner implements the RunAWS seam with aws-sdk-go-v2, so the API image
// needs no `aws` binary. It interprets only the argv cloud.InitCloudContext
// emits; anything else is an error, never a silent no-op.
type awsSDKRunner struct {
	creds awsCredentials
	// endpoint points every client at a local emulator; empty means real AWS.
	endpoint string

	mu      sync.Mutex
	configs map[string]aws.Config
}

type awsCredentials struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken,omitempty"`
}

// A missing key fails here rather than falling back to the pod's ambient identity.
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

func (r *awsSDKRunner) runAWS(ctx context.Context) func(core.Context, configpkg.CloudProviderConfig, string, []string) (string, error) {
	return func(_ core.Context, _ configpkg.CloudProviderConfig, region string, args []string) (string, error) {
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
		return dispatch(ctx, ec2.NewFromConfig(cfg), service, verb, flags, ec2Verbs)
	case "iam":
		return dispatch(ctx, iam.NewFromConfig(cfg), service, verb, flags, iamVerbs)
	case "ssm":
		return dispatch(ctx, ssm.NewFromConfig(cfg), service, verb, flags, ssmVerbs)
	default:
		return "", fmt.Errorf("aws: unsupported service %q in %v", service, args)
	}
}

func dispatch[C any](ctx context.Context, client C, service, verb string, flags awsFlags, verbs map[string]func(context.Context, C, awsFlags) (string, error)) (string, error) {
	handler, ok := verbs[verb]
	if !ok {
		return "", fmt.Errorf("aws %s: unsupported verb %q", service, verb)
	}
	return handler(ctx, client, flags)
}

// Resolving once per region keeps the bootstrap's calls off config files and IMDS.
func (r *awsSDKRunner) config(ctx context.Context, region string) (aws.Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cfg, ok := r.configs[region]; ok {
		return cfg, nil
	}
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
		// Only the alias's keys, never the ambient chain, so tenants cannot share an identity.
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

var ec2Verbs = map[string]func(context.Context, *ec2.Client, awsFlags) (string, error){
	"describe-instances":               ec2DescribeInstances,
	"create-security-group":            ec2CreateSecurityGroup,
	"describe-security-groups":         ec2DescribeSecurityGroups,
	"authorize-security-group-ingress": ec2AuthorizeIngress,
	"run-instances":                    ec2RunInstances,
	"wait":                             ec2Wait,
}

func ec2CreateSecurityGroup(ctx context.Context, client *ec2.Client, flags awsFlags) (string, error) {
	out, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(flags.one("--group-name")),
		Description: aws.String(flags.one("--description")),
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.GroupId), nil
}

func ec2DescribeSecurityGroups(ctx context.Context, client *ec2.Client, flags awsFlags) (string, error) {
	out, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupNames: flags.all("--group-names"),
	})
	if err != nil || len(out.SecurityGroups) == 0 {
		return "", err
	}
	return aws.ToString(out.SecurityGroups[0].GroupId), nil
}

func ec2AuthorizeIngress(ctx context.Context, client *ec2.Client, flags awsFlags) (string, error) {
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
}

func ec2DescribeInstances(ctx context.Context, client *ec2.Client, flags awsFlags) (string, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: flags.all("--instance-ids"),
		Filters:     parseEC2Filters(flags.all("--filters")),
	})
	if err != nil {
		return "", err
	}
	// Render only the projections cloud asks for.
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
	input, err := runInstancesInput(flags)
	if err != nil {
		return "", err
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

func runInstancesInput(flags awsFlags) (*ec2.RunInstancesInput, error) {
	count := int32(atoiOr(flags.one("--count"), 1))
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(flags.one("--image-id")),
		InstanceType: ec2types.InstanceType(flags.one("--instance-type")),
		MinCount:     aws.Int32(count),
		MaxCount:     aws.Int32(count),
	}
	// The CLI base64-encodes file:// user-data; RunInstances expects it encoded.
	userData, err := readFileArg(flags.one("--user-data"))
	if err != nil {
		return nil, err
	}
	if userData != "" {
		input.UserData = aws.String(base64.StdEncoding.EncodeToString([]byte(userData)))
	}
	applyRunInstancesShorthand(input, flags)
	applyRunInstancesReferences(input, flags)
	return input, nil
}

func applyRunInstancesShorthand(input *ec2.RunInstancesInput, flags awsFlags) {
	if mapping, ok := parseBlockDeviceMapping(flags.one("--block-device-mappings")); ok {
		input.BlockDeviceMappings = []ec2types.BlockDeviceMapping{mapping}
	}
	if options, ok := parseMetadataOptions(flags.one("--metadata-options")); ok {
		input.MetadataOptions = options
	}
	if tags, ok := parseTagSpecification(flags.one("--tag-specifications")); ok {
		input.TagSpecifications = []ec2types.TagSpecification{tags}
	}
}

func applyRunInstancesReferences(input *ec2.RunInstancesInput, flags awsFlags) {
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
}

// ec2WaitMaxDuration matches the aws CLI waiter ceiling (40 polls, 15s apart).
const ec2WaitMaxDuration = 10 * time.Minute

func ec2Wait(ctx context.Context, client *ec2.Client, flags awsFlags) (string, error) {
	input := &ec2.DescribeInstancesInput{InstanceIds: flags.all("--instance-ids")}
	switch state := flags.subcommand(); state {
	case "instance-running":
		return "", ec2.NewInstanceRunningWaiter(client).Wait(ctx, input, ec2WaitMaxDuration)
	case "instance-stopped":
		return "", ec2.NewInstanceStoppedWaiter(client).Wait(ctx, input, ec2WaitMaxDuration)
	default:
		return "", fmt.Errorf("aws ec2 wait: unsupported waiter %q", state)
	}
}

var iamVerbs = map[string]func(context.Context, *iam.Client, awsFlags) (string, error){
	"get-role":                     iamGetRole,
	"create-role":                  iamCreateRole,
	"put-role-policy":              iamPutRolePolicy,
	"get-instance-profile":         iamGetInstanceProfile,
	"create-instance-profile":      iamCreateInstanceProfile,
	"add-role-to-instance-profile": iamAddRoleToInstanceProfile,
}

func iamGetRole(ctx context.Context, client *iam.Client, flags awsFlags) (string, error) {
	out, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(flags.one("--role-name"))})
	if err != nil || out.Role == nil {
		return "", err
	}
	return aws.ToString(out.Role.RoleName), nil
}

func iamCreateRole(ctx context.Context, client *iam.Client, flags awsFlags) (string, error) {
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
}

func iamPutRolePolicy(ctx context.Context, client *iam.Client, flags awsFlags) (string, error) {
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
}

func iamGetInstanceProfile(ctx context.Context, client *iam.Client, flags awsFlags) (string, error) {
	out, err := client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(flags.one("--instance-profile-name")),
	})
	if err != nil || out.InstanceProfile == nil {
		return "", err
	}
	return instanceProfileField(out.InstanceProfile, flags.one("--query"))
}

func iamCreateInstanceProfile(ctx context.Context, client *iam.Client, flags awsFlags) (string, error) {
	out, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(flags.one("--instance-profile-name")),
	})
	if err != nil || out.InstanceProfile == nil {
		return "", err
	}
	return aws.ToString(out.InstanceProfile.Arn), nil
}

func iamAddRoleToInstanceProfile(ctx context.Context, client *iam.Client, flags awsFlags) (string, error) {
	_, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(flags.one("--instance-profile-name")),
		RoleName:            aws.String(flags.one("--role-name")),
	})
	return "", err
}

// instanceProfileField renders the profile ARN or attached role name; the role
// name decides whether a re-run must attach it again.
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

var ssmVerbs = map[string]func(context.Context, *ssm.Client, awsFlags) (string, error){
	"get-parameter": ssmGetParameter,
}

func ssmGetParameter(ctx context.Context, client *ssm.Client, flags awsFlags) (string, error) {
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(flags.one("--name"))})
	if err != nil || out.Parameter == nil {
		return "", err
	}
	return aws.ToString(out.Parameter.Value), nil
}

// awsFlags is a parsed argv tail; a leading bare token is the subcommand.
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

// parseEC2Filters parses "Name=k,Values=v1,v2" tokens.
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

func firstInstanceField(out *ec2.DescribeInstancesOutput, field func(ec2types.Instance) string) string {
	for _, reservation := range out.Reservations {
		if len(reservation.Instances) > 0 {
			return field(reservation.Instances[0])
		}
	}
	return ""
}

// describeInstancesStateLines renders the CLI's tab-separated text for
// "Reservations[*].Instances[*].[InstanceId,State.Name]".
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

// readFileArg reads a "file://<path>" argument like the aws CLI; other values pass through.
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
