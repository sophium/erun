package cloud

import (
	"sort"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
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

// CloudContextEnvLookup lets StartCloudContext enforce working hours; stores without it skip the gate.
type CloudContextEnvLookup interface {
	ListTenantConfigs() ([]model.TenantConfig, error)
	ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
}

type CloudContextDependencies struct {
	RunAWS     func(core.Context, model.CloudProviderConfig, string, []string) (string, error)
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

func ListCloudContextStatuses(store configpkg.CloudReadStore) ([]model.CloudContextStatus, error) {
	contexts, err := configpkg.ListCloudContexts(store)
	if err != nil {
		return nil, err
	}
	statuses := make([]model.CloudContextStatus, 0, len(contexts))
	for _, context := range contexts {
		statuses = append(statuses, model.CloudContextStatus{CloudContextConfig: context})
	}
	return statuses, nil
}

// RefreshCloudContextList returns contexts with live AWS status; a group whose
// AWS call fails is marked Unknown rather than left stale.
func RefreshCloudContextList(ctx core.Context, store configpkg.CloudReadStore, deps CloudContextDependencies) (model.CloudContextListResult, error) {
	statuses, err := ListCloudContextStatuses(store)
	if err != nil {
		return model.CloudContextListResult{}, err
	}
	if len(statuses) == 0 {
		return model.CloudContextListResult{CloudContexts: statuses}, nil
	}
	deps = normalizeCloudContextDependencies(deps)
	failures := refreshCloudContextStatusesFromAWS(ctx, store, deps, statuses)
	return model.CloudContextListResult{CloudContexts: statuses, RefreshFailures: failures}, nil
}

// RefreshCloudContextStatuses returns just the statuses of RefreshCloudContextList.
func RefreshCloudContextStatuses(ctx core.Context, store configpkg.CloudReadStore, deps CloudContextDependencies) ([]model.CloudContextStatus, error) {
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

func refreshCloudContextStatusesFromAWS(ctx core.Context, store configpkg.CloudReadStore, deps CloudContextDependencies, statuses []model.CloudContextStatus) []model.CloudContextRefreshFailure {
	groups := groupCloudContextRefreshIndices(statuses)
	failures := make([]model.CloudContextRefreshFailure, 0, len(groups))
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

func groupCloudContextRefreshIndices(statuses []model.CloudContextStatus) map[cloudContextRefreshKey][]int {
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

func refreshCloudContextRefreshGroup(ctx core.Context, store configpkg.CloudReadStore, deps CloudContextDependencies, statuses []model.CloudContextStatus, key cloudContextRefreshKey, indices []int) (model.CloudContextRefreshFailure, bool) {
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
	return model.CloudContextRefreshFailure{}, false
}

func applyCloudContextRefreshState(status *model.CloudContextStatus, states map[string]string) {
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
func markCloudContextsRefreshUnknown(statuses []model.CloudContextStatus, indices []int) {
	for _, i := range indices {
		statuses[i].Status = CloudContextStatusUnknown
		statuses[i].Message = ""
	}
}

func newCloudContextRefreshFailure(key cloudContextRefreshKey, err error) model.CloudContextRefreshFailure {
	return model.CloudContextRefreshFailure{
		Alias:   key.alias,
		Region:  key.region,
		Message: err.Error(),
	}
}

func describeCloudContextInstanceStates(ctx core.Context, deps CloudContextDependencies, provider model.CloudProviderConfig, region string, instanceIDs []string) (map[string]string, error) {
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
