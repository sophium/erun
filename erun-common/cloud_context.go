package eruncommon

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultCloudContextInstanceType   = "c8gd.2xlarge"
	AlternateCloudContextInstanceType = "t4g.xlarge"
	DefaultCloudContextDiskType       = "gp3"
	DefaultCloudContextDiskSizeGB     = 100
	AlternateCloudContextDiskSizeGB   = 200
	DefaultCloudContextRegion         = "eu-west-2"
	AlternateCloudContextRegion       = "eu-west-1"

	CloudContextStatusPending = "pending"
	CloudContextStatusRunning = "running"
	CloudContextStatusStopped = "stopped"
	CloudContextStatusUnknown = "unknown"
)

type CloudContextStore interface {
	CloudStore
}

// CloudContextConfig is the on-disk shape for a managed cloud context.
// It must contain only configuration the user actually authors —
// runtime/operational fields (current power state, AWS-observed status)
// belong on CloudContextStatus and are never persisted.
type CloudContextConfig struct {
	Name                string `json:"name" yaml:"name"`
	Provider            string `json:"provider" yaml:"provider"`
	CloudProviderAlias  string `json:"cloudProviderAlias" yaml:"cloudprovideralias"`
	Region              string `json:"region" yaml:"region"`
	InstanceID          string `json:"instanceId,omitempty" yaml:"instanceid,omitempty"`
	PublicIP            string `json:"publicIp,omitempty" yaml:"publicip,omitempty"`
	InstanceType        string `json:"instanceType" yaml:"instancetype"`
	DiskType            string `json:"diskType" yaml:"disktype"`
	DiskSizeGB          int    `json:"diskSizeGb" yaml:"disksizegb"`
	KubernetesContext   string `json:"kubernetesContext" yaml:"kubernetescontext"`
	SecurityGroupID     string `json:"securityGroupId,omitempty" yaml:"securitygroupid,omitempty"`
	InstanceProfileName string `json:"instanceProfileName,omitempty" yaml:"instanceprofilename,omitempty"`
	InstanceProfileARN  string `json:"instanceProfileArn,omitempty" yaml:"instanceprofilearn,omitempty"`
	InstanceRoleName    string `json:"instanceRoleName,omitempty" yaml:"instancerolename,omitempty"`
	AdminToken          string `json:"-" yaml:"admintoken,omitempty"`
	CreatedAt           string `json:"createdAt,omitempty" yaml:"createdat,omitempty"`
	UpdatedAt           string `json:"updatedAt,omitempty" yaml:"updatedat,omitempty"`
}

// CloudContextStatus pairs the persisted config with the live AWS-
// observed Status. The Status field is in-memory only: it is never
// written back to disk by any of the helpers in this package. Producers
// fill it from a Refresh* call or from the result of Init/Start/Stop;
// consumers that need a current value must ask AWS or read a cache that
// does.
//
// StopProtectionKnown / StopProtection are filled only on the
// dedicated stop-protection read/write paths
// (DescribeCloudContextStopProtection,
// SetCloudContextStopProtection) — the bulk RefreshCloudContextStatuses
// path deliberately does not fetch them so it stays one AWS call per
// (alias,region) group.
type CloudContextStatus struct {
	CloudContextConfig  `json:",inline" yaml:",inline"`
	Status              string `json:"status,omitempty" yaml:"status,omitempty"`
	Message             string `json:"message,omitempty" yaml:"message,omitempty"`
	StopProtection      bool   `json:"stopProtection,omitempty" yaml:"stopprotection,omitempty"`
	StopProtectionKnown bool   `json:"stopProtectionKnown,omitempty" yaml:"stopprotectionknown,omitempty"`
}

type InitCloudContextParams struct {
	Name               string
	CloudProviderAlias string
	Region             string
	InstanceType       string
	DiskType           string
	DiskSizeGB         int
	SubnetID           string
	SecurityGroupID    string
	KeyName            string
}

type CloudContextParams struct {
	Name  string
	Force bool
}

// CloudContextEnvLookup is an optional interface used by StartCloudContext to
// enforce per-environment working hours when starting a cloud context. Stores
// that do not implement it skip the working-hours gate.
type CloudContextEnvLookup interface {
	ListTenantConfigs() ([]TenantConfig, error)
	ListEnvConfigs(string) ([]EnvConfig, error)
}

type CloudContextDependencies struct {
	RunAWS     func(Context, CloudProviderConfig, string, []string) (string, error)
	RunKubectl func(Context, []string) error
	Now        func() time.Time
	Sleep      func(time.Duration)
	NewToken   func() string
}

func CloudContextInstanceTypes() []string {
	return []string{DefaultCloudContextInstanceType, AlternateCloudContextInstanceType}
}

func CloudContextDiskSizesGB() []int {
	return []int{DefaultCloudContextDiskSizeGB, AlternateCloudContextDiskSizeGB}
}

func CloudContextRegions() []string {
	return []string{DefaultCloudContextRegion, AlternateCloudContextRegion}
}

func NormalizeCloudContextConfig(config CloudContextConfig) CloudContextConfig {
	config.Name = strings.TrimSpace(config.Name)
	config.Provider = strings.ToLower(strings.TrimSpace(config.Provider))
	config.CloudProviderAlias = strings.TrimSpace(config.CloudProviderAlias)
	config.Region = strings.TrimSpace(config.Region)
	config.InstanceID = strings.TrimSpace(config.InstanceID)
	config.PublicIP = strings.TrimSpace(config.PublicIP)
	config.InstanceType = strings.TrimSpace(config.InstanceType)
	config.DiskType = strings.TrimSpace(config.DiskType)
	config.KubernetesContext = strings.TrimSpace(config.KubernetesContext)
	if config.Name == "" {
		config.Name = config.KubernetesContext
	}
	config.SecurityGroupID = strings.TrimSpace(config.SecurityGroupID)
	config.InstanceProfileName = strings.TrimSpace(config.InstanceProfileName)
	config.InstanceProfileARN = strings.TrimSpace(config.InstanceProfileARN)
	config.InstanceRoleName = strings.TrimSpace(config.InstanceRoleName)
	config.AdminToken = strings.TrimSpace(config.AdminToken)
	config.CreatedAt = strings.TrimSpace(config.CreatedAt)
	config.UpdatedAt = strings.TrimSpace(config.UpdatedAt)
	if config.InstanceType == "" {
		config.InstanceType = DefaultCloudContextInstanceType
	}
	if config.DiskType == "" {
		config.DiskType = DefaultCloudContextDiskType
	}
	if config.DiskSizeGB == 0 {
		config.DiskSizeGB = DefaultCloudContextDiskSizeGB
	}
	if config.KubernetesContext == "" {
		config.KubernetesContext = config.Name
	}
	return config
}

func ListCloudContexts(store CloudReadStore) ([]CloudContextConfig, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	config, _, err := store.LoadERunConfig()
	if err == ErrNotInitialized {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return normalizedCloudContexts(config.CloudContexts), nil
}

func ListCloudContextStatuses(store CloudReadStore) ([]CloudContextStatus, error) {
	contexts, err := ListCloudContexts(store)
	if err != nil {
		return nil, err
	}
	statuses := make([]CloudContextStatus, 0, len(contexts))
	for _, context := range contexts {
		statuses = append(statuses, CloudContextStatus{CloudContextConfig: context})
	}
	return statuses, nil
}

// RefreshCloudContextStatuses returns the locally configured cloud contexts
// with their Status field overwritten by the live AWS instance state. If the
// AWS describe-instances call fails for a given provider+region group, the
// affected contexts keep their cached Status and receive a Message that
// explains why the refresh did not happen.
func RefreshCloudContextStatuses(ctx Context, store CloudReadStore, deps CloudContextDependencies) ([]CloudContextStatus, error) {
	statuses, err := ListCloudContextStatuses(store)
	if err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		return statuses, nil
	}
	deps = normalizeCloudContextDependencies(deps)
	refreshCloudContextStatusesFromAWS(ctx, store, deps, statuses)
	return statuses, nil
}

type cloudContextRefreshKey struct {
	alias  string
	region string
}

func refreshCloudContextStatusesFromAWS(ctx Context, store CloudReadStore, deps CloudContextDependencies, statuses []CloudContextStatus) {
	groups := groupCloudContextRefreshIndices(statuses)
	for key, indices := range groups {
		refreshCloudContextRefreshGroup(ctx, store, deps, statuses, key, indices)
	}
}

// groupCloudContextRefreshIndices buckets the status entries by
// (alias,region) so each bucket can be refreshed with a single AWS
// describe-instances call. Entries without an instance ID, alias, or
// region keep their cached Status and are excluded from any group.
func groupCloudContextRefreshIndices(statuses []CloudContextStatus) map[cloudContextRefreshKey][]int {
	groups := make(map[cloudContextRefreshKey][]int)
	for i, status := range statuses {
		if strings.TrimSpace(status.InstanceID) == "" {
			continue
		}
		alias := strings.TrimSpace(status.CloudProviderAlias)
		region := strings.TrimSpace(status.Region)
		if alias == "" || region == "" {
			continue
		}
		key := cloudContextRefreshKey{alias: alias, region: region}
		groups[key] = append(groups[key], i)
	}
	return groups
}

// refreshCloudContextRefreshGroup resolves the provider for one
// (alias,region) group, describes its instances in a single AWS call,
// and overwrites each grouped status with the observed state. Any
// provider-resolution or describe failure downgrades the whole group to
// Unknown via applyCloudContextRefreshError so a stale "running" is not
// surfaced as authoritative.
func refreshCloudContextRefreshGroup(ctx Context, store CloudReadStore, deps CloudContextDependencies, statuses []CloudContextStatus, key cloudContextRefreshKey, indices []int) {
	provider, err := ResolveCloudProvider(store, key.alias)
	if err != nil {
		applyCloudContextRefreshError(statuses, indices, err)
		return
	}
	instanceIDs := make([]string, 0, len(indices))
	for _, i := range indices {
		instanceIDs = append(instanceIDs, statuses[i].InstanceID)
	}
	states, err := describeCloudContextInstanceStates(ctx, deps, provider, key.region, instanceIDs)
	if err != nil {
		applyCloudContextRefreshError(statuses, indices, err)
		return
	}
	for _, i := range indices {
		applyCloudContextRefreshState(&statuses[i], states)
	}
}

// applyCloudContextRefreshState overwrites a single status's Status and
// Message from the AWS-observed instance state map. A missing instance
// reads as Unknown/"instance not found in AWS"; an unrecognized state
// reads as Unknown with the raw state in the Message; a recognized state
// clears the Message.
func applyCloudContextRefreshState(status *CloudContextStatus, states map[string]string) {
	awsState, ok := states[status.InstanceID]
	if !ok {
		status.Status = CloudContextStatusUnknown
		status.Message = "instance not found in AWS"
		return
	}
	status.Status = cloudContextStatusFromAWSInstanceState(awsState)
	if status.Status == CloudContextStatusUnknown {
		status.Message = "AWS instance state: " + awsState
	} else {
		status.Message = ""
	}
}

func applyCloudContextRefreshError(statuses []CloudContextStatus, indices []int, err error) {
	// The cached Status on disk reflects the last write at create / start /
	// stop time and is not invalidated when the underlying instance is
	// changed out-of-band (AWS console, expired SSO blocking refresh, etc).
	// Downgrading to Unknown when AWS cannot be reached keeps the UI from
	// surfacing a stale "running" as authoritative.
	message := "status refresh failed: " + err.Error()
	for _, i := range indices {
		statuses[i].Status = CloudContextStatusUnknown
		statuses[i].Message = message
	}
}

func describeCloudContextInstanceStates(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region string, instanceIDs []string) (map[string]string, error) {
	args := []string{
		"ec2", "describe-instances",
		"--filters", "Name=instance-id,Values=" + strings.Join(instanceIDs, ","),
		"--query", "Reservations[*].Instances[*].[InstanceId,State.Name]",
		"--output", "text",
	}
	out, err := deps.RunAWS(ctx, provider, region, args)
	if err != nil {
		return nil, err
	}
	return parseCloudContextInstanceStates(out), nil
}

func parseCloudContextInstanceStates(out string) map[string]string {
	states := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		instanceID := strings.TrimSpace(fields[0])
		state := strings.ToLower(strings.TrimSpace(fields[1]))
		if instanceID == "" || state == "" {
			continue
		}
		states[instanceID] = state
	}
	return states
}

func cloudContextStatusFromAWSInstanceState(awsState string) string {
	switch strings.ToLower(strings.TrimSpace(awsState)) {
	case "running":
		return CloudContextStatusRunning
	case "pending":
		return CloudContextStatusPending
	case "stopping", "stopped":
		return CloudContextStatusStopped
	default:
		return CloudContextStatusUnknown
	}
}

func InitCloudContext(ctx Context, store CloudContextStore, params InitCloudContextParams, deps CloudContextDependencies) (CloudContextStatus, error) {
	if store == nil {
		return CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	ctx.Trace(fmt.Sprintf("cloud-context init: alias=%s region=%s instance-type=%s disk=%dGB/%s",
		strings.TrimSpace(params.CloudProviderAlias),
		strings.TrimSpace(params.Region),
		strings.TrimSpace(params.InstanceType),
		params.DiskSizeGB,
		strings.TrimSpace(params.DiskType)))
	provider, config, err := initCloudContextConfig(store, params, deps)
	if err != nil {
		ctx.Trace("cloud-context init: configuration resolution failed: " + err.Error())
		return CloudContextStatus{}, err
	}
	ctx.Trace(fmt.Sprintf("cloud-context init: resolved name=%s kube-context=%s", config.Name, config.KubernetesContext))
	if err := prepareInitCloudContextResources(ctx, deps, provider, params, &config); err != nil {
		return CloudContextStatus{}, err
	}

	instanceID, err := runInitCloudContextInstance(ctx, deps, provider, params, &config)
	if err != nil {
		return CloudContextStatus{}, err
	}
	config.InstanceID = strings.TrimSpace(instanceID)
	if config.InstanceID == "" {
		config.InstanceID = "i-<new-instance>"
	}

	if err := finalizeInitCloudContext(ctx, deps, provider, &config); err != nil {
		return CloudContextStatus{}, err
	}
	// finalizeInitCloudContext invokes `aws ec2 wait instance-running`
	// before returning, so a successful return means the instance is
	// running. In dry-run mode the wait is stubbed but the intent of
	// the command is the same — report the resolved end-state to the
	// caller.
	status := CloudContextStatusRunning
	if ctx.DryRun {
		return CloudContextStatus{CloudContextConfig: NormalizeCloudContextConfig(config), Status: status}, nil
	}
	if err := saveCloudContextConfig(store, config); err != nil {
		return CloudContextStatus{}, err
	}
	return CloudContextStatus{CloudContextConfig: NormalizeCloudContextConfig(config), Status: status}, nil
}

func initCloudContextConfig(store CloudContextStore, params InitCloudContextParams, deps CloudContextDependencies) (CloudProviderConfig, CloudContextConfig, error) {
	provider, err := ResolveCloudProvider(store, params.CloudProviderAlias)
	if err != nil {
		return CloudProviderConfig{}, CloudContextConfig{}, err
	}
	if provider.Provider != CloudProviderAWS {
		return CloudProviderConfig{}, CloudContextConfig{}, fmt.Errorf("unsupported cloud provider %q", provider.Provider)
	}
	existingContexts, err := ListCloudContexts(store)
	if err != nil {
		return CloudProviderConfig{}, CloudContextConfig{}, err
	}
	config, err := resolveInitCloudContextConfig(provider, params, deps.Now(), existingContexts)
	if err != nil {
		return CloudProviderConfig{}, CloudContextConfig{}, err
	}
	if err := ensureInitCloudContextDoesNotExist(store, config.Name); err != nil {
		return CloudProviderConfig{}, CloudContextConfig{}, err
	}
	return provider, config, nil
}

func ensureInitCloudContextDoesNotExist(store CloudContextStore, name string) error {
	existing, ok, err := findCloudContext(store, name)
	if err != nil {
		return err
	}
	if ok && existing.InstanceID != "" {
		return fmt.Errorf("cloud context %q already exists", name)
	}
	return nil
}

func prepareInitCloudContextResources(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, params InitCloudContextParams, config *CloudContextConfig) error {
	securityGroupID, err := initCloudContextSecurityGroup(ctx, deps, provider, params, *config)
	if err != nil {
		return err
	}
	config.SecurityGroupID = securityGroupID
	instanceProfile, err := ensureCloudContextInstanceProfile(ctx, deps, provider, config.Region, config.Name)
	if err != nil {
		return err
	}
	config.InstanceProfileName = instanceProfile.Name
	config.InstanceProfileARN = instanceProfile.ARN
	config.InstanceRoleName = instanceProfile.RoleName
	return nil
}

func initCloudContextSecurityGroup(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, params InitCloudContextParams, config CloudContextConfig) (string, error) {
	securityGroupID := strings.TrimSpace(params.SecurityGroupID)
	if securityGroupID != "" {
		return securityGroupID, nil
	}
	return createCloudContextSecurityGroup(ctx, deps, provider, config.Region, config.Name)
}

func runInitCloudContextInstance(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, params InitCloudContextParams, config *CloudContextConfig) (string, error) {
	// Idempotency (issue #605): a re-run — a durable provisioning workflow
	// resuming after a crash, or a re-issued `erun context init` — must not
	// launch a duplicate instance. Reuse the instance already tagged for this
	// context, if one is still pending/running. The admin token comes from
	// deps.NewToken(); when the caller derives it deterministically (the hosted
	// provisioner does, from the context id), the reused instance's baked token
	// and the re-resolved token agree.
	existingID, err := findRunningCloudContextInstance(ctx, deps, provider, config.Region, config.Name)
	if err != nil {
		return "", err
	}
	config.AdminToken = deps.NewToken()
	if existingID != "" {
		ctx.Trace("cloud-context init: reusing existing instance " + existingID + " for context " + config.Name)
		return existingID, nil
	}
	ami, err := initCloudContextAMI(ctx, deps, provider, config.Region)
	if err != nil {
		return "", err
	}
	userDataPath, cleanup, err := cloudContextUserDataFile(ctx, config.AdminToken)
	if err != nil {
		return "", err
	}
	defer cleanup()
	args := initCloudContextRunArgs(ami, userDataPath, params, *config)
	return runAWSWithIAMConsistencyRetry(ctx, deps, provider, config.Region, args)
}

// findRunningCloudContextInstance returns the id of an instance already tagged
// for this context and still pending/running, or "" when none exists. It makes
// InitCloudContext idempotent: a re-run reuses the instance instead of launching
// a duplicate. In dry-run the traced query returns empty, so the plan proceeds
// to the launch step unchanged (aside from the new query trace line).
func findRunningCloudContextInstance(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, name string) (string, error) {
	out, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "describe-instances",
		"--filters",
		"Name=tag:erun:context,Values=" + name,
		"Name=instance-state-name,Values=pending,running",
		"--query", "Reservations[0].Instances[0].InstanceId",
		"--output", "text",
	})
	if err != nil {
		return "", err
	}
	// Dry-run models a fresh provision so the plan shows the full launch: the
	// query is traced above for auditability, but its stubbed result is ignored.
	if ctx.DryRun {
		return "", nil
	}
	id := strings.TrimSpace(out)
	if id == "" || strings.EqualFold(id, "none") {
		return "", nil
	}
	return id, nil
}

const iamConsistencyMaxAttempts = 6

var iamConsistencyBackoff = []time.Duration{
	2 * time.Second,
	4 * time.Second,
	6 * time.Second,
	8 * time.Second,
	10 * time.Second,
}

func runAWSWithIAMConsistencyRetry(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region string, args []string) (string, error) {
	var (
		out string
		err error
	)
	for attempt := 0; attempt < iamConsistencyMaxAttempts; attempt++ {
		out, err = deps.RunAWS(ctx, provider, region, args)
		if err == nil {
			return out, nil
		}
		if !isIAMInstanceProfileConsistencyError(err) || attempt == iamConsistencyMaxAttempts-1 {
			return out, err
		}
		ctx.Trace(fmt.Sprintf("IAM instance profile not yet visible to EC2; retrying in %s", iamConsistencyBackoff[attempt]))
		deps.Sleep(iamConsistencyBackoff[attempt])
	}
	return out, err
}

func isIAMInstanceProfileConsistencyError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Invalid IAM Instance Profile name") ||
		strings.Contains(message, "Invalid IAM Instance Profile ARN")
}

func initCloudContextAMI(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region string) (string, error) {
	ami, err := deps.RunAWS(ctx, provider, region, []string{
		"ssm", "get-parameter",
		"--name", "/aws/service/canonical/ubuntu/server/24.04/stable/current/arm64/hvm/ebs-gp3/ami-id",
		"--query", "Parameter.Value",
		"--output", "text",
	})
	if err != nil {
		return "", err
	}
	if ami = strings.TrimSpace(ami); ami != "" {
		return ami, nil
	}
	return "ami-<latest-ubuntu-arm64>", nil
}

func initCloudContextRunArgs(ami, userDataPath string, params InitCloudContextParams, config CloudContextConfig) []string {
	runArgs := []string{
		"ec2", "run-instances",
		"--image-id", ami,
		"--instance-type", config.InstanceType,
		"--count", "1",
		"--block-device-mappings", fmt.Sprintf("DeviceName=/dev/sda1,Ebs={VolumeSize=%d,VolumeType=%s,DeleteOnTermination=true}", config.DiskSizeGB, config.DiskType),
		"--user-data", "file://" + userDataPath,
		"--metadata-options", "HttpEndpoint=enabled,HttpTokens=required,HttpPutResponseHopLimit=2",
		"--tag-specifications", fmt.Sprintf("ResourceType=instance,Tags=[{Key=Name,Value=%s},{Key=erun:context,Value=%s}]", config.Name, config.Name),
		"--query", "Instances[0].InstanceId",
		"--output", "text",
	}
	runArgs = appendOptionalCloudContextRunArg(runArgs, "--security-group-ids", config.SecurityGroupID)
	runArgs = appendCloudContextProfileRunArg(runArgs, config)
	runArgs = appendOptionalCloudContextRunArg(runArgs, "--subnet-id", params.SubnetID)
	return appendOptionalCloudContextRunArg(runArgs, "--key-name", params.KeyName)
}

func appendOptionalCloudContextRunArg(args []string, name, value string) []string {
	if value = strings.TrimSpace(value); value != "" {
		return append(args, name, value)
	}
	return args
}

func appendCloudContextProfileRunArg(args []string, config CloudContextConfig) []string {
	if config.InstanceProfileName != "" {
		return append(args, "--iam-instance-profile", "Name="+config.InstanceProfileName)
	}
	if config.InstanceProfileARN != "" {
		return append(args, "--iam-instance-profile", "Arn="+config.InstanceProfileARN)
	}
	return args
}

func finalizeInitCloudContext(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, config *CloudContextConfig) error {
	if _, err := deps.RunAWS(ctx, provider, config.Region, []string{"ec2", "wait", "instance-running", "--instance-ids", config.InstanceID}); err != nil {
		return err
	}
	publicIP, err := describeCloudContextPublicIP(ctx, deps, provider, config.Region, config.InstanceID)
	if err != nil {
		return err
	}
	config.PublicIP = publicIP
	if err := configureCloudKubeContext(ctx, deps, *config); err != nil {
		return err
	}
	config.UpdatedAt = deps.Now().UTC().Format(time.RFC3339)
	return nil
}

func StopCloudContext(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) (CloudContextStatus, error) {
	deps = normalizeCloudContextDependencies(deps)
	ctx.Trace("cloud-context stop: " + strings.TrimSpace(params.Name))
	status, err := changeCloudContextPowerState(ctx, store, params, deps, "stop-instances", CloudContextStatusStopped)
	switch {
	case err == nil:
	case isAWSIncorrectInstanceStateError(err):
		// AWS rejects stop-instances when the instance is already not
		// running (already-stopping, already-stopped, terminating, …).
		// For our purposes that's the intended end state — fall through
		// to the wait below so the function only returns once AWS
		// observes the instance fully stopped.
		ctx.Trace("cloud-context stop: instance is already in a non-running state — waiting for stopped")
		status, err = resolveCloudContextStatusForName(store, params.Name)
		if err != nil {
			return CloudContextStatus{}, err
		}
	default:
		return CloudContextStatus{}, err
	}
	provider, err := ResolveCloudProvider(store, status.CloudProviderAlias)
	if err != nil {
		return CloudContextStatus{}, err
	}
	// stop-instances accepts the action but the EC2 then transitions
	// running → stopping → stopped over ~30-60 s. Until #361, this
	// helper returned the instant AWS acknowledged stop-instances,
	// which lied about the live state: a follow-up `cloud-context
	// start` issued in the next few seconds would race the
	// transition and AWS would reply IncorrectInstanceState. Wait
	// for the observed end state before reporting success so callers
	// (the desktop's idle stopper notification, `erun open` preflight,
	// delete-env teardown) only see "stopped" once the instance has
	// actually got there.
	if _, err := deps.RunAWS(ctx, provider, status.Region, []string{"ec2", "wait", "instance-stopped", "--instance-ids", status.InstanceID}); err != nil {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q: stop was accepted but the instance was not observed stopped — it may still be transitioning; check its state in the AWS console and retry: %w", status.Name, classifyCloudContextPowerError("stop-instances", status.CloudContextConfig, err))
	}
	return status, nil
}

// resolveCloudContextStatusForName re-reads the persisted config so
// StopCloudContext can recover the InstanceID/Region/Alias needed to
// run `aws ec2 wait instance-stopped` when the initial stop-instances
// call short-circuited with IncorrectInstanceState.
func resolveCloudContextStatusForName(store CloudContextStore, name string) (CloudContextStatus, error) {
	config, ok, err := findCloudContext(store, name)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if !ok {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(name))
	}
	return CloudContextStatus{
		CloudContextConfig: NormalizeCloudContextConfig(config),
		Status:             CloudContextStatusStopped,
	}, nil
}

// isAWSIncorrectInstanceStateError reports whether err is the AWS
// IncorrectInstanceState response that stop-instances and
// start-instances return when the target instance is in a state the
// requested transition cannot apply to (e.g. starting a stopping
// instance, stopping an already-stopped instance). The default
// RunAWS wraps the underlying CLI error in a "aws ec2 ...: ..."
// string, so a substring check on the raw stderr is the most stable
// signal across AWS CLI versions.
func isAWSIncorrectInstanceStateError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "IncorrectInstanceState")
}

// awsExpiredCredentialsMarkers are the stderr signatures the AWS CLI emits
// when the call failed for authentication rather than the requested
// operation: an expired/invalid SSO session, expired STS credentials, or no
// credentials at all. Substring matching on the wrapped error mirrors
// isAWSIncorrectInstanceStateError.
var awsExpiredCredentialsMarkers = []string{
	"Token has expired",
	"SSO session associated with this profile has expired",
	"ExpiredToken",
	"RequestExpired",
	"Unable to locate credentials",
	"InvalidClientTokenId",
	"AuthFailure",
}

func isAWSExpiredCredentialsError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	for _, marker := range awsExpiredCredentialsMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// classifyCloudContextPowerError translates a raw start-instances /
// stop-instances failure into the actionable reason the operator needs
// (issue #456: a failed Stop surfaced as a bare exit 1 while the instance
// kept running). The two failure families with a known next step:
//
//   - OperationNotPermitted on stop — stop protection (DisableApiStop) is
//     on, the deliberate repair-time lock; the fix is unlocking it, not
//     retrying.
//   - Expired/missing AWS credentials — the call never reached the
//     instance; the fix is signing in again.
//
// Anything else passes through unchanged so callers (including the
// IncorrectInstanceState absorption in StopCloudContext/StartCloudContext)
// keep seeing the raw AWS text.
func classifyCloudContextPowerError(awsAction string, config CloudContextConfig, err error) error {
	switch {
	case err == nil:
		return nil
	case awsAction == "stop-instances" && strings.Contains(err.Error(), "OperationNotPermitted"):
		return fmt.Errorf("cloud context %q cannot be stopped: stop protection (DisableApiStop) is enabled on instance %s — turn it off first (`erun context enable-api-stop %s`, or the stop-protection toggle in the desktop titlebar), then retry: %w", config.Name, config.InstanceID, config.Name, err)
	case isAWSExpiredCredentialsError(err):
		return fmt.Errorf("cloud context %q: the AWS session for alias %q is expired or unavailable — sign in again (`erun cloud login --alias %s`, or the cloud login in the desktop settings), then retry: %w", config.Name, config.CloudProviderAlias, config.CloudProviderAlias, err)
	}
	return err
}

func StartCloudContext(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) (CloudContextStatus, error) {
	deps = normalizeCloudContextDependencies(deps)
	ctx.Trace(fmt.Sprintf("cloud-context start: name=%s force=%v", strings.TrimSpace(params.Name), params.Force))
	if err := enforceCloudContextStartWorkingHoursGate(ctx, store, params, deps); err != nil {
		return CloudContextStatus{}, err
	}
	if err := ensureCloudContextHostStopProfileAssociation(ctx, store, params, deps); err != nil {
		ctx.Trace("skipping cloud context host-stop profile association: " + err.Error())
	}
	status, err := changeCloudContextPowerState(ctx, store, params, deps, "start-instances", CloudContextStatusRunning)
	if err != nil {
		status, err = recoverCloudContextStartFromTransitionalState(ctx, store, params, deps, err)
		if err != nil {
			return CloudContextStatus{}, err
		}
	}
	return finalizeStartedCloudContext(ctx, store, deps, status)
}

// finalizeStartedCloudContext brings a just-started instance fully up
// and persists the result: it waits for instance-running, refreshes the
// public IP, reconfigures the kube context, stamps UpdatedAt, and saves
// the config (skipping the save in dry-run). This is Start's own
// follow-up save that changeCloudContextPowerState deliberately leaves
// to the caller.
func finalizeStartedCloudContext(ctx Context, store CloudContextStore, deps CloudContextDependencies, status CloudContextStatus) (CloudContextStatus, error) {
	deps = normalizeCloudContextDependencies(deps)
	provider, err := ResolveCloudProvider(store, status.CloudProviderAlias)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if _, err := deps.RunAWS(ctx, provider, status.Region, []string{"ec2", "wait", "instance-running", "--instance-ids", status.InstanceID}); err != nil {
		return CloudContextStatus{}, err
	}
	publicIP, err := describeCloudContextPublicIP(ctx, deps, provider, status.Region, status.InstanceID)
	if err != nil {
		return CloudContextStatus{}, err
	}
	status.PublicIP = publicIP
	if err := configureCloudKubeContext(ctx, deps, status.CloudContextConfig); err != nil {
		return CloudContextStatus{}, err
	}
	status.UpdatedAt = deps.Now().UTC().Format(time.RFC3339)
	if ctx.DryRun {
		return status, nil
	}
	if err := saveCloudContextConfig(store, status.CloudContextConfig); err != nil {
		return CloudContextStatus{}, err
	}
	return status, nil
}

// enforceCloudContextStartWorkingHoursGate applies the per-environment
// working-hours gate before a start. force=true bypasses it; a store
// that does not implement CloudContextEnvLookup skips it. When every
// attached environment is outside its working hours the start is
// rejected with the force-to-override hint.
func enforceCloudContextStartWorkingHoursGate(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) error {
	if params.Force {
		ctx.Trace("cloud-context start: force=true bypasses working-hours gate")
		return nil
	}
	lookup, ok := store.(CloudContextEnvLookup)
	if !ok {
		return nil
	}
	ctx.Trace("cloud-context start: checking working-hours gate")
	reason, err := cloudContextStartBlockedByWorkingHours(store, lookup, params.Name, deps.Now())
	if err != nil {
		return err
	}
	if reason != "" {
		ctx.Trace("cloud-context start: gated: " + reason)
		return fmt.Errorf("cloud context %q cannot start: %s; pass force=true to override", strings.TrimSpace(params.Name), reason)
	}
	ctx.Trace("cloud-context start: working-hours gate clear")
	return nil
}

// recoverCloudContextStartFromTransitionalState handles a failed
// start-instances. A non-IncorrectInstanceState failure is returned
// unchanged. IncorrectInstanceState means the instance is in a
// transitional state — almost always `stopping`, sometimes `pending`
// or `shutting-down`. start-instances only accepts fully-stopped
// instances. Wait for the transition to settle and retry once. Without
// this recovery the user's click on an env whose linked context just
// auto-stopped fails with a confusing AWS error and the desktop's
// reconnect loop spins pointlessly. See issue #361.
func recoverCloudContextStartFromTransitionalState(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies, startErr error) (CloudContextStatus, error) {
	if !isAWSIncorrectInstanceStateError(startErr) {
		return CloudContextStatus{}, startErr
	}
	ctx.Trace("cloud-context start: instance is in a transitional state — waiting for stopped before retrying start-instances")
	recovered, recoverErr := resolveCloudContextStatusForName(store, params.Name)
	if recoverErr != nil {
		return CloudContextStatus{}, recoverErr
	}
	provider, perr := ResolveCloudProvider(store, recovered.CloudProviderAlias)
	if perr != nil {
		return CloudContextStatus{}, perr
	}
	if _, werr := deps.RunAWS(ctx, provider, recovered.Region, []string{"ec2", "wait", "instance-stopped", "--instance-ids", recovered.InstanceID}); werr != nil {
		return CloudContextStatus{}, werr
	}
	return changeCloudContextPowerState(ctx, store, params, deps, "start-instances", CloudContextStatusRunning)
}

// CloudContextStopProtectionParams identifies the cloud context whose
// AWS DisableApiStop attribute should be flipped. Enabled=true sets
// DisableApiStop=true (the instance cannot be stopped by any caller
// until the attribute is cleared), Enabled=false reverses it.
type CloudContextStopProtectionParams struct {
	Name    string
	Enabled bool
}

// SetCloudContextStopProtection toggles the EC2 DisableApiStop attribute
// for the named cloud context. When Enabled=true, every subsequent
// ec2:StopInstances call (the in-pod idle monitor, the desktop Stop
// button, any external script) returns OperationNotPermitted until the
// attribute is cleared. This is the recovery lever used when an env's
// in-pod components are unhealthy and the user needs to keep the
// underlying EC2 up long enough to repair them.
//
// The function does not gate on the instance's current power state —
// DisableApiStop is settable on running and stopped instances. The
// returned status carries StopProtectionKnown=true and StopProtection
// reflects the new value so callers can render the result without an
// extra describe round-trip.
func SetCloudContextStopProtection(ctx Context, store CloudContextStore, params CloudContextStopProtectionParams, deps CloudContextDependencies) (CloudContextStatus, error) {
	if store == nil {
		return CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	verb := "disable"
	if !params.Enabled {
		verb = "enable"
	}
	ctx.Trace(fmt.Sprintf("cloud-context stop-protection %s: %s", verb, strings.TrimSpace(params.Name)))
	config, ok, err := findCloudContext(store, params.Name)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if !ok {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(params.Name))
	}
	if config.InstanceID == "" {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q has no instance ID", config.Name)
	}
	provider, err := ResolveCloudProvider(store, config.CloudProviderAlias)
	if err != nil {
		return CloudContextStatus{}, err
	}
	flag := "--disable-api-stop"
	if !params.Enabled {
		flag = "--no-disable-api-stop"
	}
	if _, err := deps.RunAWS(ctx, provider, config.Region, []string{"ec2", "modify-instance-attribute", "--instance-id", config.InstanceID, flag}); err != nil {
		return CloudContextStatus{}, err
	}
	return CloudContextStatus{
		CloudContextConfig:  NormalizeCloudContextConfig(config),
		StopProtection:      params.Enabled,
		StopProtectionKnown: true,
	}, nil
}

// DescribeCloudContextStopProtection reads the live DisableApiStop
// attribute for the named cloud context. It is a separate call from
// the bulk RefreshCloudContextStatuses path because the AWS attribute
// is read via describe-instance-attribute (one call per instance),
// not describe-instances; calling it for every context on every
// refresh would multiply the AWS round-trip count by the number of
// configured envs. The desktop calls it lazily for the env whose
// detail view is open.
func DescribeCloudContextStopProtection(ctx Context, store CloudContextStore, name string, deps CloudContextDependencies) (CloudContextStatus, error) {
	if store == nil {
		return CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	config, ok, err := findCloudContext(store, name)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if !ok {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(name))
	}
	if config.InstanceID == "" {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q has no instance ID", config.Name)
	}
	provider, err := ResolveCloudProvider(store, config.CloudProviderAlias)
	if err != nil {
		return CloudContextStatus{}, err
	}
	out, err := deps.RunAWS(ctx, provider, config.Region, []string{
		"ec2", "describe-instance-attribute",
		"--instance-id", config.InstanceID,
		"--attribute", "disableApiStop",
		"--query", "DisableApiStop.Value",
		"--output", "text",
	})
	if err != nil {
		return CloudContextStatus{}, err
	}
	enabled := parseCloudContextStopProtectionOutput(out)
	return CloudContextStatus{
		CloudContextConfig:  NormalizeCloudContextConfig(config),
		StopProtection:      enabled,
		StopProtectionKnown: true,
	}, nil
}

// parseCloudContextStopProtectionOutput reads the `aws ec2
// describe-instance-attribute --query DisableApiStop.Value --output text`
// response. AWS returns "True" / "False" (capitalized) for the boolean
// attribute, or an empty string when --dry-run short-circuits the call.
// Anything other than a recognized "true" reads as disabled so the
// failure mode of an unknown response is the safer of the two.
func parseCloudContextStopProtectionOutput(out string) bool {
	switch strings.ToLower(strings.TrimSpace(out)) {
	case "true":
		return true
	default:
		return false
	}
}

func ensureCloudContextHostStopProfileAssociation(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) error {
	if store == nil {
		return fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	config, ok, err := findCloudContext(store, params.Name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(params.Name))
	}
	if strings.TrimSpace(config.InstanceID) == "" {
		return nil
	}
	provider, err := ResolveCloudProvider(store, config.CloudProviderAlias)
	if err != nil {
		return err
	}
	instanceProfile, err := ensureCloudContextInstanceProfile(ctx, deps, provider, config.Region, config.Name)
	if err != nil {
		return err
	}
	profileRef := "Name=" + instanceProfile.Name
	if instanceProfile.ARN != "" {
		profileRef = "Arn=" + instanceProfile.ARN
	}
	if err := ensureCloudContextInstanceProfileAssociation(ctx, deps, provider, config.Region, config.InstanceID, profileRef); err != nil {
		return err
	}

	config.InstanceProfileName = instanceProfile.Name
	config.InstanceProfileARN = instanceProfile.ARN
	config.InstanceRoleName = instanceProfile.RoleName
	if ctx.DryRun {
		return nil
	}
	return saveCloudContextConfig(store, config)
}

func ensureCloudContextInstanceProfileAssociation(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID, profileRef string) error {
	deps = normalizeCloudContextDependencies(deps)
	associationID, err := activeCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, instanceID)
	if err != nil {
		return err
	}
	if associationID != "" {
		associationARN, err := activeCloudContextInstanceProfileAssociationARN(ctx, deps, provider, region, instanceID)
		if err != nil {
			return err
		}
		if profileRefMatchesAssociation(profileRef, associationARN) {
			return nil
		}
		return replaceCloudContextInstanceProfileAssociation(ctx, deps, provider, region, instanceID, associationID, profileRef)
	}

	pendingAssociationID, err := pendingCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, instanceID)
	if err != nil {
		return err
	}
	if pendingAssociationID != "" {
		return nil
	}

	if _, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "associate-iam-instance-profile",
		"--instance-id", instanceID,
		"--iam-instance-profile", profileRef,
	}); err != nil {
		if !isAlreadyAssociatedAWSError(err) && !isExistingInstanceProfileAssociationError(err) {
			return err
		}
		ctx.Trace("cloud-context: instance profile already associated with " + instanceID + " — reusing the existing association")
	}
	return nil
}

func activeCloudContextInstanceProfileAssociationID(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID string) (string, error) {
	return describeCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, []string{
		"Name=instance-id,Values=" + instanceID,
		"Name=state,Values=associated",
	})
}

func activeCloudContextInstanceProfileAssociationARN(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID string) (string, error) {
	return describeCloudContextInstanceProfileAssociationARN(ctx, deps, provider, region, []string{
		"Name=instance-id,Values=" + instanceID,
		"Name=state,Values=associated",
	})
}

func pendingCloudContextInstanceProfileAssociationID(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID string) (string, error) {
	return describeCloudContextInstanceProfileAssociationID(ctx, deps, provider, region, []string{
		"Name=instance-id,Values=" + instanceID,
		"Name=state,Values=associating,disassociating",
	})
}

func describeCloudContextInstanceProfileAssociationID(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region string, filters []string) (string, error) {
	args := []string{
		"ec2", "describe-iam-instance-profile-associations",
		"--filters",
	}
	args = append(args, filters...)
	args = append(args,
		"--query", "IamInstanceProfileAssociations[0].AssociationId",
		"--output", "text",
	)
	associationID, err := deps.RunAWS(ctx, provider, region, args)
	if err != nil {
		return "", err
	}
	associationID = strings.TrimSpace(associationID)
	if associationID == "" || strings.EqualFold(associationID, "none") {
		return "", nil
	}
	return associationID, nil
}

func describeCloudContextInstanceProfileAssociationARN(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region string, filters []string) (string, error) {
	args := []string{
		"ec2", "describe-iam-instance-profile-associations",
		"--filters",
	}
	args = append(args, filters...)
	args = append(args,
		"--query", "IamInstanceProfileAssociations[0].IamInstanceProfile.Arn",
		"--output", "text",
	)
	arn, err := deps.RunAWS(ctx, provider, region, args)
	if err != nil {
		return "", err
	}
	arn = strings.TrimSpace(arn)
	if arn == "" || strings.EqualFold(arn, "none") {
		return "", nil
	}
	return arn, nil
}

func profileRefMatchesAssociation(profileRef, associationARN string) bool {
	profileRef = strings.TrimSpace(profileRef)
	associationARN = strings.TrimSpace(associationARN)
	return associationARN != "" && strings.TrimPrefix(profileRef, "Arn=") == associationARN
}

func replaceCloudContextInstanceProfileAssociation(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID, associationID, profileRef string) error {
	associationID = strings.TrimSpace(associationID)
	if associationID == "" {
		return nil
	}
	_, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "replace-iam-instance-profile-association",
		"--association-id", associationID,
		"--iam-instance-profile", profileRef,
	})
	return err
}

// refreshSingleCloudContextStatus runs RefreshCloudContextStatuses and
// returns the entry matching the supplied seed's name. Used by paths
// that need a current AWS-observed Status for a known context — the
// persisted config does not carry Status, so callers cannot fall back
// to a cached value on disk.
func refreshSingleCloudContextStatus(ctx Context, store CloudReadStore, deps CloudContextDependencies, seed CloudContextStatus) (CloudContextStatus, error) {
	statuses, err := RefreshCloudContextStatuses(ctx, store, deps)
	if err != nil {
		return CloudContextStatus{}, err
	}
	name := strings.TrimSpace(seed.Name)
	for _, status := range statuses {
		if strings.TrimSpace(status.Name) == name {
			return status, nil
		}
	}
	return seed, nil
}

func CloudContextPreflight(store CloudContextStore, deps CloudContextDependencies) KubernetesContextPreflightFunc {
	var mu sync.Mutex
	started := make(map[string]struct{})
	return func(ctx Context, kubernetesContext string) error {
		kubernetesContext = strings.TrimSpace(kubernetesContext)
		if kubernetesContext == "" || store == nil {
			return nil
		}

		mu.Lock()
		if _, ok := started[kubernetesContext]; ok {
			mu.Unlock()
			return nil
		}
		mu.Unlock()

		status, ok, err := findCloudContextForKubernetesContext(store, kubernetesContext)
		if err != nil || !ok {
			return err
		}
		// Persisted config no longer carries Status, so reach AWS for
		// the authoritative current state before deciding whether to
		// start. CloudContextPreflight is invoked at most once per
		// context per CLI run (see the in-closure `started` cache), so
		// one describe-instances call here is the right cost-quality
		// trade.
		live, err := refreshSingleCloudContextStatus(ctx, store, deps, status)
		if err != nil {
			return err
		}
		if strings.TrimSpace(live.Status) == CloudContextStatusRunning {
			return nil
		}

		if _, err := StartCloudContext(ctx, store, CloudContextParams{Name: status.Name}, deps); err != nil {
			return err
		}

		mu.Lock()
		started[kubernetesContext] = struct{}{}
		if name := strings.TrimSpace(status.Name); name != "" {
			started[name] = struct{}{}
		}
		mu.Unlock()
		return nil
	}
}

// cloudContextStartBlockedByWorkingHours returns a non-empty reason when every
// environment attached to the named cloud context is currently outside its
// working hours. If any attached environment permits start, or no environments
// reference this context, the gate is not engaged.
func cloudContextStartBlockedByWorkingHours(store CloudReadStore, lookup CloudContextEnvLookup, contextName string, now time.Time) (string, error) {
	kubeContext, ok, err := resolveCloudContextWorkingHoursKubeContext(store, contextName)
	if err != nil || !ok {
		return "", err
	}

	tenants, err := lookup.ListTenantConfigs()
	if err != nil {
		return "", err
	}
	var blockedReason string
	hasAttachedEnv := false
	for _, tenant := range tenants {
		envs, err := lookup.ListEnvConfigs(tenant.Name)
		if err != nil {
			return "", err
		}
		permitsStart, tenantReason := evaluateTenantWorkingHoursGate(tenant, envs, kubeContext, now, &hasAttachedEnv)
		if permitsStart {
			return "", nil
		}
		if blockedReason == "" {
			blockedReason = tenantReason
		}
	}
	if !hasAttachedEnv {
		return "", nil
	}
	return blockedReason, nil
}

// resolveCloudContextWorkingHoursKubeContext loads the named cloud
// context and returns the kube-context name the working-hours gate
// matches environments against (falling back to the context's own Name).
// ok=false (with a nil error) means there is nothing to gate — a blank
// name, an unknown context, or a context with no resolvable kube-context
// — and the caller should treat the gate as not engaged.
func resolveCloudContextWorkingHoursKubeContext(store CloudReadStore, contextName string) (string, bool, error) {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return "", false, nil
	}
	config, ok, err := findCloudContext(store, contextName)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	kubeContext := strings.TrimSpace(config.KubernetesContext)
	if kubeContext == "" {
		kubeContext = strings.TrimSpace(config.Name)
	}
	if kubeContext == "" {
		return "", false, nil
	}
	return kubeContext, true, nil
}

// evaluateTenantWorkingHoursGate inspects every environment in one
// tenant that is attached to kubeContext. It sets *hasAttachedEnv when
// it finds any attached env, returns permitsStart=true the moment an
// attached env is inside its working hours (the caller treats that as
// "gate clear" and stops), and otherwise returns the first
// outside-working-hours reason it observed. Environments whose idle
// policy or working-hours status cannot be resolved are skipped, matching
// the original inline behavior.
func evaluateTenantWorkingHoursGate(tenant TenantConfig, envs []EnvConfig, kubeContext string, now time.Time, hasAttachedEnv *bool) (bool, string) {
	var blockedReason string
	for _, env := range envs {
		if strings.TrimSpace(env.KubernetesContext) != kubeContext {
			continue
		}
		*hasAttachedEnv = true
		policy, err := env.Idle.Resolve()
		if err != nil {
			continue
		}
		outside, _, err := workingHoursStatus(policy.WorkingHours, policy.Timezone, now)
		if err != nil {
			continue
		}
		if !outside {
			return true, ""
		}
		if blockedReason == "" {
			blockedReason = fmt.Sprintf("outside working hours %s for environment %s/%s", policy.WorkingHours, tenant.Name, env.Name)
		}
	}
	return false, blockedReason
}

func findCloudContextForKubernetesContext(store CloudReadStore, kubernetesContext string) (CloudContextStatus, bool, error) {
	kubernetesContext = strings.TrimSpace(kubernetesContext)
	if kubernetesContext == "" {
		return CloudContextStatus{}, false, nil
	}
	contexts, err := ListCloudContexts(store)
	if err != nil {
		return CloudContextStatus{}, false, err
	}
	for _, context := range contexts {
		context = NormalizeCloudContextConfig(context)
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			return CloudContextStatus{CloudContextConfig: context}, true, nil
		}
	}
	return CloudContextStatus{}, false, nil
}

// changeCloudContextPowerState runs the AWS power-state mutation and
// returns a CloudContextStatus whose Status field reflects the intended
// new state. It does not persist the config: the persisted shape no
// longer carries Status, so a Stop has nothing to write. Start performs
// its own follow-up save (PublicIP refresh + UpdatedAt) after this
// helper returns.
func changeCloudContextPowerState(ctx Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies, awsAction, status string) (CloudContextStatus, error) {
	if store == nil {
		return CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	config, ok, err := findCloudContext(store, params.Name)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if !ok {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(params.Name))
	}
	if config.InstanceID == "" {
		return CloudContextStatus{}, fmt.Errorf("cloud context %q has no instance ID", config.Name)
	}
	provider, err := ResolveCloudProvider(store, config.CloudProviderAlias)
	if err != nil {
		return CloudContextStatus{}, err
	}
	if _, err := deps.RunAWS(ctx, provider, config.Region, []string{"ec2", awsAction, "--instance-ids", config.InstanceID}); err != nil {
		return CloudContextStatus{}, classifyCloudContextPowerError(awsAction, config, err)
	}
	return CloudContextStatus{CloudContextConfig: NormalizeCloudContextConfig(config), Status: status}, nil
}

func resolveInitCloudContextConfig(provider CloudProviderConfig, params InitCloudContextParams, now time.Time, existingContexts []CloudContextConfig) (CloudContextConfig, error) {
	config := CloudContextConfig{
		Name:               strings.TrimSpace(params.Name),
		Provider:           CloudProviderAWS,
		CloudProviderAlias: provider.Alias,
		Region:             strings.TrimSpace(params.Region),
		InstanceType:       strings.TrimSpace(params.InstanceType),
		DiskType:           strings.TrimSpace(params.DiskType),
		DiskSizeGB:         params.DiskSizeGB,
		CreatedAt:          now.UTC().Format(time.RFC3339),
		UpdatedAt:          now.UTC().Format(time.RFC3339),
	}
	if config.Region == "" {
		config.Region = DefaultCloudContextRegion
	}
	if config.Name == "" {
		config.Name = generatedCloudContextName(provider, config.Region, existingContexts)
	}
	config = NormalizeCloudContextConfig(config)
	if config.CloudProviderAlias == "" {
		return CloudContextConfig{}, fmt.Errorf("cloud provider alias is required")
	}
	if config.Region == "" {
		return CloudContextConfig{}, fmt.Errorf("cloud context region is required")
	}
	if !validCloudContextRegion(config.Region) {
		return CloudContextConfig{}, fmt.Errorf("unsupported cloud context region %q", config.Region)
	}
	if !validCloudContextInstanceType(config.InstanceType) {
		return CloudContextConfig{}, fmt.Errorf("unsupported cloud context instance type %q", config.InstanceType)
	}
	if !validCloudContextDiskSize(config.DiskSizeGB) {
		return CloudContextConfig{}, fmt.Errorf("unsupported cloud context disk size %d", config.DiskSizeGB)
	}
	if config.DiskType != DefaultCloudContextDiskType {
		return CloudContextConfig{}, fmt.Errorf("unsupported cloud context disk type %q", config.DiskType)
	}
	return config, nil
}

func createCloudContextSecurityGroup(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, name string) (string, error) {
	groupName := name + "-k3s"
	groupID, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "create-security-group",
		"--group-name", groupName,
		"--description", "ERun managed k3s API access for " + name,
		"--query", "GroupId",
		"--output", "text",
	})
	if err != nil {
		if !isDuplicateSecurityGroupError(err) {
			return "", err
		}
		ctx.Trace("cloud-context: security group " + groupName + " already exists — reusing it")
		groupID, err = describeCloudContextSecurityGroupID(ctx, deps, provider, region, groupName)
		if err != nil {
			return "", err
		}
	}
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		groupID = "sg-<" + name + ">"
	}
	_, err = deps.RunAWS(ctx, provider, region, []string{
		"ec2", "authorize-security-group-ingress",
		"--group-id", groupID,
		"--protocol", "tcp",
		"--port", "6443",
		"--cidr", "0.0.0.0/0",
	})
	if err != nil {
		if !isDuplicateSecurityGroupPermissionError(err) {
			return "", err
		}
		ctx.Trace("cloud-context: k3s API ingress rule already present on " + groupID + " — reusing it")
	}
	return groupID, nil
}

func describeCloudContextSecurityGroupID(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, groupName string) (string, error) {
	return deps.RunAWS(ctx, provider, region, []string{
		"ec2", "describe-security-groups",
		"--group-names", groupName,
		"--query", "SecurityGroups[0].GroupId",
		"--output", "text",
	})
}

type cloudContextInstanceProfile struct {
	Name     string
	ARN      string
	RoleName string
}

type cloudContextPolicyPaths struct {
	Trust  string
	Policy string
}

func ensureCloudContextInstanceProfile(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, name string) (cloudContextInstanceProfile, error) {
	roleName := cloudContextInstanceRoleName(name)
	profileName := cloudContextInstanceProfileName(name)
	if roleName == "" || profileName == "" {
		return cloudContextInstanceProfile{}, fmt.Errorf("cloud context name is required")
	}

	policies, cleanup, err := cloudContextInstanceProfilePolicyPaths(ctx, provider, region, name)
	if err != nil {
		return cloudContextInstanceProfile{}, err
	}
	defer cleanup()

	if ctx.DryRun {
		return dryRunCloudContextInstanceProfile(ctx, deps, provider, region, profileName, roleName, policies)
	}
	return realCloudContextInstanceProfile(ctx, deps, provider, region, profileName, roleName, policies)
}

func cloudContextInstanceProfilePolicyPaths(ctx Context, provider CloudProviderConfig, region, name string) (cloudContextPolicyPaths, func(), error) {
	trustPath, cleanupTrust, err := cloudContextPolicyFile(ctx, ec2AssumeRolePolicy())
	if err != nil {
		return cloudContextPolicyPaths{}, func() {}, err
	}
	policyPath, cleanupPolicy, err := cloudContextPolicyFile(ctx, cloudContextSelfStopPolicy(provider, region, name))
	if err != nil {
		cleanupTrust()
		return cloudContextPolicyPaths{}, func() {}, err
	}
	cleanup := func() {
		cleanupPolicy()
		cleanupTrust()
	}
	return cloudContextPolicyPaths{Trust: trustPath, Policy: policyPath}, cleanup, nil
}

func dryRunCloudContextInstanceProfile(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, profileName, roleName string, policies cloudContextPolicyPaths) (cloudContextInstanceProfile, error) {
	commands := [][]string{
		{"iam", "create-role", "--role-name", roleName, "--assume-role-policy-document", "file://" + policies.Trust, "--query", "Role.RoleName", "--output", "text"},
		{"iam", "put-role-policy", "--role-name", roleName, "--policy-name", "erun-self-stop", "--policy-document", "file://" + policies.Policy},
		{"iam", "create-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Arn", "--output", "text"},
		{"iam", "add-role-to-instance-profile", "--instance-profile-name", profileName, "--role-name", roleName},
	}
	for _, command := range commands {
		if _, err := deps.RunAWS(ctx, provider, region, command); err != nil {
			return cloudContextInstanceProfile{}, err
		}
	}
	return cloudContextInstanceProfile{Name: profileName, ARN: cloudContextInstanceProfileARN(provider, profileName), RoleName: roleName}, nil
}

func realCloudContextInstanceProfile(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, profileName, roleName string, policies cloudContextPolicyPaths) (cloudContextInstanceProfile, error) {
	if err := ensureCloudContextInstanceRole(ctx, deps, provider, region, roleName, policies.Trust); err != nil {
		return cloudContextInstanceProfile{}, err
	}
	if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "put-role-policy", "--role-name", roleName, "--policy-name", "erun-self-stop", "--policy-document", "file://" + policies.Policy}); err != nil {
		return cloudContextInstanceProfile{}, err
	}
	profileARN, createdProfile, err := ensureCloudContextInstanceProfileExists(ctx, deps, provider, region, profileName)
	if err != nil {
		return cloudContextInstanceProfile{}, err
	}
	if err := ensureCloudContextInstanceProfileRole(ctx, deps, provider, region, profileName, roleName, createdProfile); err != nil {
		return cloudContextInstanceProfile{}, err
	}

	profileARN = strings.TrimSpace(profileARN)
	if profileARN == "" || strings.EqualFold(profileARN, "none") {
		profileARN = cloudContextInstanceProfileARN(provider, profileName)
	}
	return cloudContextInstanceProfile{
		Name:     profileName,
		ARN:      profileARN,
		RoleName: roleName,
	}, nil
}

func ensureCloudContextInstanceRole(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, roleName, trustPath string) error {
	if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "get-role", "--role-name", roleName, "--query", "Role.RoleName", "--output", "text"}); err == nil {
		return nil
	}
	_, err := deps.RunAWS(ctx, provider, region, []string{"iam", "create-role", "--role-name", roleName, "--assume-role-policy-document", "file://" + trustPath, "--query", "Role.RoleName", "--output", "text"})
	return err
}

func ensureCloudContextInstanceProfileExists(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, profileName string) (string, bool, error) {
	profileARN, err := deps.RunAWS(ctx, provider, region, []string{"iam", "get-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Arn", "--output", "text"})
	if err == nil {
		return profileARN, false, nil
	}
	profileARN, err = deps.RunAWS(ctx, provider, region, []string{"iam", "create-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Arn", "--output", "text"})
	return profileARN, true, err
}

func ensureCloudContextInstanceProfileRole(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, profileName, roleName string, createdProfile bool) error {
	addRole, err := shouldAddCloudContextProfileRole(ctx, deps, provider, region, profileName, roleName, createdProfile)
	if err != nil {
		return err
	}
	if !addRole {
		return nil
	}

	if _, err := deps.RunAWS(ctx, provider, region, []string{"iam", "add-role-to-instance-profile", "--instance-profile-name", profileName, "--role-name", roleName}); err != nil {
		if !isInstanceProfileRoleLimitError(err) && !isAlreadyAssociatedAWSError(err) {
			return err
		}
		ctx.Trace("cloud-context: instance profile " + profileName + " already carries a role — checking it matches " + roleName)
		existingRole, roleErr := cloudContextInstanceProfileRoleName(ctx, deps, provider, region, profileName)
		if roleErr != nil {
			return err
		}
		if existingRole == roleName {
			return nil
		}
		if existingRole != "" {
			return fmt.Errorf("instance profile %q already contains role %q; expected %q", profileName, existingRole, roleName)
		}
		return err
	}
	return nil
}

func shouldAddCloudContextProfileRole(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, profileName, roleName string, createdProfile bool) (bool, error) {
	if createdProfile {
		return true, nil
	}
	existingRole, err := cloudContextInstanceProfileRoleName(ctx, deps, provider, region, profileName)
	if err != nil {
		return false, err
	}
	if existingRole == roleName {
		return false, nil
	}
	if existingRole != "" {
		return false, fmt.Errorf("instance profile %q already contains role %q; expected %q", profileName, existingRole, roleName)
	}
	return true, nil
}

func cloudContextInstanceProfileRoleName(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, profileName string) (string, error) {
	roleName, err := deps.RunAWS(ctx, provider, region, []string{"iam", "get-instance-profile", "--instance-profile-name", profileName, "--query", "InstanceProfile.Roles[0].RoleName", "--output", "text"})
	if err != nil {
		return "", err
	}
	roleName = strings.TrimSpace(roleName)
	if roleName == "" || strings.EqualFold(roleName, "none") || strings.EqualFold(roleName, "null") {
		return "", nil
	}
	return roleName, nil
}

func ec2AssumeRolePolicy() map[string]any {
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect": "Allow",
			"Principal": map[string]string{
				"Service": "ec2.amazonaws.com",
			},
			"Action": "sts:AssumeRole",
		}},
	}
}

func cloudContextSelfStopPolicy(provider CloudProviderConfig, region, name string) map[string]any {
	accountID := strings.TrimSpace(provider.AccountID)
	if accountID == "" {
		accountID = "*"
	}
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Sid":      "AllowSelfStop",
				"Effect":   "Allow",
				"Action":   "ec2:StopInstances",
				"Resource": fmt.Sprintf("arn:aws:ec2:%s:%s:instance/*", region, accountID),
				"Condition": map[string]any{
					"StringEquals": map[string]string{
						"ec2:ResourceTag/erun:context": name,
					},
				},
			},
			{
				"Sid":    "AllowBedrockClaudeCode",
				"Effect": "Allow",
				"Action": []string{
					"bedrock:InvokeModel",
					"bedrock:InvokeModelWithResponseStream",
					"bedrock:ListInferenceProfiles",
					"bedrock:GetInferenceProfile",
				},
				"Resource": []string{
					"arn:aws:bedrock:*:*:inference-profile/*",
					"arn:aws:bedrock:*:*:application-inference-profile/*",
					"arn:aws:bedrock:*:*:foundation-model/*",
				},
			},
			{
				"Sid":    "AllowBedrockMarketplaceAccess",
				"Effect": "Allow",
				"Action": []string{
					"aws-marketplace:ViewSubscriptions",
					"aws-marketplace:Subscribe",
				},
				"Resource": "*",
				"Condition": map[string]any{
					"StringEquals": map[string]string{
						"aws:CalledViaLast": "bedrock.amazonaws.com",
					},
				},
			},
		},
	}
}

func cloudContextPolicyFile(ctx Context, policy map[string]any) (string, func(), error) {
	if ctx.DryRun {
		return "<generated-iam-policy>", func() {}, nil
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp("", "erun-cloud-context-policy-*.json")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(file.Name())
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return file.Name(), cleanup, nil
}

func cloudContextInstanceRoleName(name string) string {
	return cloudContextInstanceProfileBaseName(name)
}

func cloudContextInstanceProfileName(name string) string {
	return cloudContextInstanceProfileBaseName(name)
}

func cloudContextInstanceProfileBaseName(name string) string {
	name = sanitizeIAMName(name)
	if !strings.HasPrefix(name, "erun-") {
		name = "erun-" + name
	}
	return truncateIAMName(name + "-host-stop")
}

func cloudContextInstanceProfileARN(provider CloudProviderConfig, profileName string) string {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return ""
	}
	accountID := strings.TrimSpace(provider.AccountID)
	if accountID == "" {
		return ""
	}
	return "arn:aws:iam::" + accountID + ":instance-profile/" + profileName
}

func sanitizeIAMName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if isValidIAMNameRune(r) {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func isValidIAMNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' ||
		r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' ||
		strings.ContainsRune("+=,.@_-", r)
}

func truncateIAMName(value string) string {
	value = strings.Trim(value, "-")
	if len(value) <= 64 {
		return value
	}
	return strings.TrimRight(value[:64], "-")
}

func isAlreadyAssociatedAWSError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already") || strings.Contains(message, "limitexceeded")
}

func isInstanceProfileRoleLimitError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "limitexceeded") && strings.Contains(message, "addroletoinstanceprofile")
}

func isExistingInstanceProfileAssociationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "incorrectstate") && strings.Contains(message, "existing association")
}

func isDuplicateSecurityGroupError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "invalidgroup.duplicate")
}

func isDuplicateSecurityGroupPermissionError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "invalidpermission.duplicate")
}

func describeCloudContextPublicIP(ctx Context, deps CloudContextDependencies, provider CloudProviderConfig, region, instanceID string) (string, error) {
	publicIP, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "describe-instances",
		"--instance-ids", instanceID,
		"--query", "Reservations[0].Instances[0].PublicIpAddress",
		"--output", "text",
	})
	if err != nil {
		return "", err
	}
	publicIP = strings.TrimSpace(publicIP)
	if publicIP == "" || strings.EqualFold(publicIP, "none") {
		if ctx.DryRun {
			return "203.0.113.10", nil
		}
		return "", fmt.Errorf("cloud context instance %q does not have a public IP yet", instanceID)
	}
	return publicIP, nil
}

func configureCloudKubeContext(ctx Context, deps CloudContextDependencies, config CloudContextConfig) error {
	config = NormalizeCloudContextConfig(config)
	if config.PublicIP == "" {
		return fmt.Errorf("cloud context public IP is required")
	}
	if config.AdminToken == "" {
		return fmt.Errorf("cloud context admin token is required")
	}
	commands := [][]string{
		{"config", "set-cluster", config.KubernetesContext, "--server", "https://" + config.PublicIP + ":6443", "--insecure-skip-tls-verify=true"},
		{"config", "set-credentials", config.KubernetesContext, "--token", config.AdminToken},
		{"config", "set-context", config.KubernetesContext, "--cluster", config.KubernetesContext, "--user", config.KubernetesContext},
	}
	for _, args := range commands {
		if err := deps.RunKubectl(ctx, args); err != nil {
			return err
		}
	}
	return nil
}

func cloudContextUserDataFile(ctx Context, adminToken string) (string, func(), error) {
	if ctx.DryRun {
		return "<generated-k3s-user-data>", func() {}, nil
	}
	file, err := os.CreateTemp("", "erun-k3s-user-data-*.sh")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(file.Name())
	}
	userData := `#!/bin/sh
set -eu
mkdir -p /etc/rancher/k3s
cat >/etc/rancher/k3s/token-auth.csv <<'EOF'
` + adminToken + `,erun-admin,erun-admin,"system:masters"
EOF
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="server --kube-apiserver-arg=token-auth-file=/etc/rancher/k3s/token-auth.csv" sh -
`
	if _, err := file.WriteString(userData); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return file.Name(), cleanup, nil
}

func saveCloudContextConfig(store CloudContextStore, context CloudContextConfig) error {
	context = NormalizeCloudContextConfig(context)
	config, _, err := store.LoadERunConfig()
	if err == ErrNotInitialized {
		config = ERunConfig{}
	} else if err != nil {
		return err
	}
	config.CloudContexts = upsertCloudContext(config.CloudContexts, context)
	return store.SaveERunConfig(config)
}

func findCloudContext(store CloudReadStore, name string) (CloudContextConfig, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CloudContextConfig{}, false, fmt.Errorf("cloud context name is required")
	}
	contexts, err := ListCloudContexts(store)
	if err != nil {
		return CloudContextConfig{}, false, err
	}
	for _, context := range contexts {
		if context.Name == name || context.KubernetesContext == name {
			return context, true, nil
		}
	}
	return CloudContextConfig{}, false, nil
}

func upsertCloudContext(contexts []CloudContextConfig, context CloudContextConfig) []CloudContextConfig {
	context = NormalizeCloudContextConfig(context)
	updated := false
	result := make([]CloudContextConfig, 0, len(contexts)+1)
	for _, existing := range contexts {
		existing = NormalizeCloudContextConfig(existing)
		if existing.Name == "" {
			continue
		}
		if existing.Name == context.Name {
			result = append(result, context)
			updated = true
			continue
		}
		result = append(result, existing)
	}
	if !updated {
		result = append(result, context)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func normalizedCloudContexts(contexts []CloudContextConfig) []CloudContextConfig {
	result := make([]CloudContextConfig, 0, len(contexts))
	for _, context := range contexts {
		context = NormalizeCloudContextConfig(context)
		if context.Name == "" {
			continue
		}
		result = append(result, context)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func validCloudContextInstanceType(value string) bool {
	for _, option := range CloudContextInstanceTypes() {
		if value == option {
			return true
		}
	}
	return false
}

func validCloudContextDiskSize(value int) bool {
	for _, option := range CloudContextDiskSizesGB() {
		if value == option {
			return true
		}
	}
	return false
}

func validCloudContextRegion(value string) bool {
	for _, option := range CloudContextRegions() {
		if value == option {
			return true
		}
	}
	return false
}

func generatedCloudContextName(provider CloudProviderConfig, region string, existingContexts []CloudContextConfig) string {
	parts := make([]string, 0, 2)
	if provider.AccountID != "" {
		parts = append(parts, provider.AccountID)
	} else if provider.Username != "" {
		parts = append(parts, provider.Username)
	} else {
		parts = append(parts, provider.Alias)
	}
	if region != "" {
		parts = append(parts, region)
	}
	tail := sanitizeCloudContextName(strings.Join(parts, "-"))
	return nextCloudContextName(tail, existingContexts)
}

func nextCloudContextName(tail string, existingContexts []CloudContextConfig) string {
	tail = sanitizeCloudContextName(tail)
	if tail == "" {
		tail = "context"
	}
	prefix := "erun-"
	suffix := "-" + tail
	next := 1
	for _, context := range existingContexts {
		for _, name := range []string{context.Name, context.KubernetesContext} {
			name = strings.TrimSpace(name)
			if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
				continue
			}
			counter := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
			if len(counter) != 3 {
				continue
			}
			value, err := strconv.Atoi(counter)
			if err == nil && value >= next {
				next = value + 1
			}
		}
	}
	return fmt.Sprintf("erun-%03d-%s", next, tail)
}

func sanitizeCloudContextName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func normalizeCloudContextDependencies(deps CloudContextDependencies) CloudContextDependencies {
	if deps.RunAWS == nil {
		deps.RunAWS = defaultRunCloudContextAWS
	}
	if deps.RunKubectl == nil {
		deps.RunKubectl = defaultRunCloudContextKubectl
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Sleep == nil {
		deps.Sleep = time.Sleep
	}
	if deps.NewToken == nil {
		deps.NewToken = newCloudContextToken
	}
	return deps
}

func defaultRunCloudContextAWS(ctx Context, provider CloudProviderConfig, region string, args []string) (string, error) {
	fullArgs := append([]string(nil), args...)
	if region = strings.TrimSpace(region); region != "" && !containsAWSFlag(fullArgs, "--region") {
		fullArgs = append(fullArgs, "--region", region)
	}
	if profile := strings.TrimSpace(provider.Profile); profile != "" && !containsAWSFlag(fullArgs, "--profile") {
		fullArgs = append(fullArgs, "--profile", profile)
	}
	ctx.TraceCommand("", "aws", fullArgs...)
	if ctx.DryRun {
		return dryRunAWSOutput(args), nil
	}
	var stdout bytes.Buffer
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := RawCommandRunner("", "aws", fullArgs, nil, &stdout, stderr); err != nil {
		return "", fmt.Errorf("aws %s: %s", strings.Join(args, " "), commandErrorMessage(err, stderrBuffer.String(), "AWS command failed"))
	}
	return stdout.String(), nil
}

func defaultRunCloudContextKubectl(ctx Context, args []string) error {
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	stdout, _ := captureWriter(ctx.Stdout)
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := RawCommandRunner("", "kubectl", args, nil, stdout, stderr); err != nil {
		return fmt.Errorf("kubectl %s: %s", strings.Join(args, " "), commandErrorMessage(err, stderrBuffer.String(), "kubectl command failed"))
	}
	return nil
}

func containsAWSFlag(args []string, flag string) bool {
	for i, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
		if i > 0 && args[i-1] == flag {
			return true
		}
	}
	return false
}

func dryRunAWSOutput(args []string) string {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "ssm get-parameter"):
		return "ami-<latest-ubuntu-arm64>\n"
	case strings.Contains(joined, "ec2 create-security-group"):
		return "sg-<cloud-context>\n"
	case strings.Contains(joined, "ec2 run-instances"):
		return "i-<new-instance>\n"
	case strings.Contains(joined, "ec2 describe-instances"):
		// Two describe-instances callers in the cloud-context code:
		// one queries [InstanceId,State.Name] (status refresh, parsed
		// as two-column text), the other queries PublicIpAddress.
		// Default the status-refresh output to "running" so dry-run
		// callers see the common-case end-state; tests that want to
		// trace the stopped path should supply a custom RunAWS.
		if strings.Contains(joined, "[InstanceId,State.Name]") {
			return dryRunDescribeInstanceStateOutput(joined)
		}
		return "203.0.113.10\n"
	default:
		return ""
	}
}

// dryRunDescribeInstanceStateOutput renders a `--query
// [InstanceId,State.Name]` response for each --filters Values=...
// argument the caller supplied. Defaulting every instance to "running"
// keeps the dry-run preflight from spuriously starting a context that
// in real life would already be up. Tests that need to exercise the
// start path should supply a custom RunAWS that returns "stopped".
func dryRunDescribeInstanceStateOutput(joined string) string {
	const marker = "Values="
	idx := strings.Index(joined, marker)
	if idx < 0 {
		return ""
	}
	rest := joined[idx+len(marker):]
	if cut := strings.Index(rest, " "); cut >= 0 {
		rest = rest[:cut]
	}
	rest = strings.Trim(rest, "'\"")
	if rest == "" {
		return ""
	}
	var buf strings.Builder
	for _, id := range strings.Split(rest, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		buf.WriteString(id)
		buf.WriteString("\trunning\n")
	}
	return buf.String()
}

func newCloudContextToken() string {
	token := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, token); err != nil {
		return "erun-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(token)
}
