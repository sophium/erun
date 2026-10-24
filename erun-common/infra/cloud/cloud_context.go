package cloud

import (
	"sort"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

const (
	AlternateCloudContextInstanceType = "t4g.xlarge"
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

// CloudContextRefreshFailure is one failed batched status refresh, recorded
// once per (alias, region) group rather than copied onto each context.
type CloudContextRefreshFailure struct {
	Alias   string `json:"alias" yaml:"alias"`
	Region  string `json:"region" yaml:"region"`
	Message string `json:"message" yaml:"message"`
}

// CloudContextListResult is the shared result shape of the CLI's --output json and the context_list MCP tool.
// CloudContexts has no omitempty so an empty list serialises as []; RefreshFailures
// keeps it because the refresh is never attempted when there are no contexts.
type CloudContextListResult struct {
	CloudContexts   []configpkg.CloudContextStatus `json:"cloudContexts"`
	RefreshFailures []CloudContextRefreshFailure   `json:"refreshFailures,omitempty"`
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

// CloudContextEnvLookup lets StartCloudContext enforce working hours; stores without it skip the gate.
type CloudContextEnvLookup interface {
	ListTenantConfigs() ([]configpkg.TenantConfig, error)
	ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
}

type CloudContextDependencies struct {
	RunAWS     func(core.Context, configpkg.CloudProviderConfig, string, []string) (string, error)
	RunKubectl func(core.Context, []string) error
	Now        func() time.Time
	Sleep      func(time.Duration)
	NewToken   func() string
}

func CloudContextInstanceTypes() []string {
	return []string{configpkg.DefaultCloudContextInstanceType, AlternateCloudContextInstanceType}
}

func CloudContextDiskSizesGB() []int {
	return []int{configpkg.DefaultCloudContextDiskSizeGB, AlternateCloudContextDiskSizeGB}
}

func CloudContextRegions() []string {
	return []string{DefaultCloudContextRegion, AlternateCloudContextRegion}
}

func ListCloudContextStatuses(store configpkg.CloudReadStore) ([]configpkg.CloudContextStatus, error) {
	contexts, err := configpkg.ListCloudContexts(store)
	if err != nil {
		return nil, err
	}
	statuses := make([]configpkg.CloudContextStatus, 0, len(contexts))
	for _, context := range contexts {
		statuses = append(statuses, configpkg.CloudContextStatus{CloudContextConfig: context})
	}
	return statuses, nil
}

// RefreshCloudContextList returns contexts with live AWS status; a group whose
// AWS call fails is marked Unknown rather than left stale.
func RefreshCloudContextList(ctx core.Context, store configpkg.CloudReadStore, deps CloudContextDependencies) (CloudContextListResult, error) {
	statuses, err := ListCloudContextStatuses(store)
	if err != nil {
		return CloudContextListResult{}, err
	}
	if len(statuses) == 0 {
		return CloudContextListResult{CloudContexts: statuses}, nil
	}
	deps = normalizeCloudContextDependencies(deps)
	failures := refreshCloudContextStatusesFromAWS(ctx, store, deps, statuses)
	return CloudContextListResult{CloudContexts: statuses, RefreshFailures: failures}, nil
}

// RefreshCloudContextStatuses returns just the statuses of RefreshCloudContextList.
func RefreshCloudContextStatuses(ctx core.Context, store configpkg.CloudReadStore, deps CloudContextDependencies) ([]configpkg.CloudContextStatus, error) {
	result, err := RefreshCloudContextList(ctx, store, deps)
	if err != nil {
		return nil, err
	}
	return result.CloudContexts, nil
}

type cloudContextRefreshKey struct {
	alias  string
	region string
}

func refreshCloudContextStatusesFromAWS(ctx core.Context, store configpkg.CloudReadStore, deps CloudContextDependencies, statuses []configpkg.CloudContextStatus) []CloudContextRefreshFailure {
	groups := groupCloudContextRefreshIndices(statuses)
	failures := make([]CloudContextRefreshFailure, 0, len(groups))
	for key, indices := range groups {
		if failure, failed := refreshCloudContextRefreshGroup(ctx, store, deps, statuses, key, indices); failed {
			failures = append(failures, failure)
		}
	}
	// Groups come from a map; sort for stable output.
	sort.Slice(failures, func(i, j int) bool {
		if failures[i].Alias != failures[j].Alias {
			return failures[i].Alias < failures[j].Alias
		}
		return failures[i].Region < failures[j].Region
	})
	return failures
}

func groupCloudContextRefreshIndices(statuses []configpkg.CloudContextStatus) map[cloudContextRefreshKey][]int {
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

func refreshCloudContextRefreshGroup(ctx core.Context, store configpkg.CloudReadStore, deps CloudContextDependencies, statuses []configpkg.CloudContextStatus, key cloudContextRefreshKey, indices []int) (CloudContextRefreshFailure, bool) {
	provider, err := ResolveCloudProvider(store, key.alias)
	if err != nil {
		markCloudContextsRefreshUnknown(statuses, indices)
		return newCloudContextRefreshFailure(key, err), true
	}
	instanceIDs := make([]string, 0, len(indices))
	for _, i := range indices {
		instanceIDs = append(instanceIDs, statuses[i].InstanceID)
	}
	states, err := describeCloudContextInstanceStates(ctx, deps, provider, key.region, instanceIDs)
	if err != nil {
		markCloudContextsRefreshUnknown(statuses, indices)
		return newCloudContextRefreshFailure(key, err), true
	}
	for _, i := range indices {
		applyCloudContextRefreshState(&statuses[i], states)
	}
	return CloudContextRefreshFailure{}, false
}

func applyCloudContextRefreshState(status *configpkg.CloudContextStatus, states map[string]string) {
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

// markCloudContextsRefreshUnknown keeps a stale "running" from looking authoritative;
// the cause is reported once on the group's CloudContextRefreshFailure.
func markCloudContextsRefreshUnknown(statuses []configpkg.CloudContextStatus, indices []int) {
	for _, i := range indices {
		statuses[i].Status = CloudContextStatusUnknown
		statuses[i].Message = ""
	}
}

func newCloudContextRefreshFailure(key cloudContextRefreshKey, err error) CloudContextRefreshFailure {
	return CloudContextRefreshFailure{
		Alias:   key.alias,
		Region:  key.region,
		Message: err.Error(),
	}
}

func describeCloudContextInstanceStates(ctx core.Context, deps CloudContextDependencies, provider configpkg.CloudProviderConfig, region string, instanceIDs []string) (map[string]string, error) {
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
