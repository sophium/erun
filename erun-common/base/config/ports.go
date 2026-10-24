package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

const (
	LowerServicePort         = 17000
	EnvironmentPortRangeSize = 100

	MCPServicePortOffset           = 0
	SSHServicePortOffset           = 22
	APIServicePortOffset           = 33
	ContributeAppServicePortOffset = 50

	MCPServicePort           = LowerServicePort + MCPServicePortOffset
	APIServicePort           = LowerServicePort + APIServicePortOffset
	DefaultSSHLocalPort      = LowerServicePort + SSHServicePortOffset
	ContributeAppServicePort = LowerServicePort + ContributeAppServicePortOffset
)

type EnvironmentLocalPorts struct {
	RangeStart    int `json:"rangeStart,omitempty"`
	RangeEnd      int `json:"rangeEnd,omitempty"`
	MCP           int `json:"mcp,omitempty"`
	API           int `json:"api,omitempty"`
	SSH           int `json:"ssh,omitempty"`
	ContributeApp int `json:"contributeApp,omitempty"`
}

type EnvironmentPortStore interface {
	ListTenantConfigs() ([]TenantConfig, error)
	ListEnvConfigs(string) ([]EnvConfig, error)
}

// LocalPortRangeOverlapError reports two envs persisting the same range start;
// it is not reassigned because the persisted value is operator-owned.
type LocalPortRangeOverlapError struct {
	A          string
	B          string
	RangeStart int
}

func (e LocalPortRangeOverlapError) Error() string {
	return fmt.Sprintf("local port range start %d is claimed by both %s and %s; edit localportrangestart in one of the env configs to resolve", e.RangeStart, e.A, e.B)
}

func EnvironmentLocalPortsFromRangeStart(rangeStart int) EnvironmentLocalPorts {
	if rangeStart <= 0 {
		return EnvironmentLocalPorts{}
	}
	return EnvironmentLocalPorts{
		RangeStart:    rangeStart,
		RangeEnd:      rangeStart + EnvironmentPortRangeSize - 1,
		MCP:           rangeStart + MCPServicePortOffset,
		API:           rangeStart + APIServicePortOffset,
		SSH:           rangeStart + SSHServicePortOffset,
		ContributeApp: rangeStart + ContributeAppServicePortOffset,
	}
}

// OverlayInjectedRuntimeLocalPorts prefers the chart-injected ports of this
// process's own environment over the on-disk projection, which may lack
// localportrangestart. The injected MCP port is the range start by construction.
func OverlayInjectedRuntimeLocalPorts(ports EnvironmentLocalPorts, env func(string) string, tenant, environment string) EnvironmentLocalPorts {
	podTenant, podEnvironment, ok := core.InjectedRuntimePodIdentity(env)
	if !ok || podTenant != strings.TrimSpace(tenant) || podEnvironment != strings.TrimSpace(environment) {
		return ports
	}
	rangeStart := injectedPortValue(env("ERUN_MCP_PORT"))
	if _, err := EnvironmentPortIndexForRangeStart(rangeStart, EnvironmentPortKey(tenant, environment)); err != nil {
		return ports
	}
	injected := EnvironmentLocalPortsFromRangeStart(rangeStart)
	if sshPort := injectedPortValue(env("ERUN_SSHD_PORT")); sshPort > 0 {
		injected.SSH = sshPort
	} else {
		injected.SSH = ports.SSH
	}
	return injected
}

func injectedPortValue(value string) int {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return port
}

// LocalPortsForResult derives local ports, preferring injected ports, then a
// persisted range start. Zero is deliberate: a missing range must fail loudly.
func LocalPortsForResult(result OpenResult) EnvironmentLocalPorts {
	var ports EnvironmentLocalPorts
	switch {
	case result.EnvConfig.LocalPortRangeStart > 0:
		ports = EnvironmentLocalPortsFromRangeStart(result.EnvConfig.LocalPortRangeStart)
	case result.LocalPorts.RangeStart > 0:
		ports = result.LocalPorts
	}
	if ports.RangeStart > 0 {
		if ports.MCP == 0 {
			ports.MCP = ports.RangeStart + MCPServicePortOffset
		}
		if ports.API == 0 {
			ports.API = ports.RangeStart + APIServicePortOffset
		}
		if ports.SSH == 0 {
			ports.SSH = ports.RangeStart + SSHServicePortOffset
		}
		if ports.ContributeApp == 0 {
			ports.ContributeApp = ports.RangeStart + ContributeAppServicePortOffset
		}
	}
	if result.EnvConfig.SSHD.LocalPort > 0 {
		ports.SSH = result.EnvConfig.SSHD.LocalPort
	}
	return OverlayInjectedRuntimeLocalPorts(ports, os.Getenv, result.Tenant, result.Environment)
}

func MCPPortForResult(result OpenResult) int {
	return LocalPortsForResult(result).MCP
}

func APIPortForResult(result OpenResult) int {
	return LocalPortsForResult(result).API
}

// APIServicePortForResult returns the per-environment API Service port deploy
// renders as apiPort, falling back to the chart's 17033. Dial this, not APIServicePort.
func APIServicePortForResult(result OpenResult) int {
	if port := LocalPortsForResult(result).API; port > 0 {
		return port
	}
	return APIServicePort
}

func SSHLocalPortForResult(result OpenResult) int {
	return LocalPortsForResult(result).SSH
}

// ContributeAppPortForResult returns the contribute-app port, bound only in contribute mode.
func ContributeAppPortForResult(result OpenResult) int {
	return LocalPortsForResult(result).ContributeApp
}

type environmentPortRef struct {
	Key string
	Env EnvConfig
}

func CollectEnvironmentPortRefs(store EnvironmentPortStore) (persisted, unpersisted []environmentPortRef, err error) {
	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(tenants, func(i, j int) bool {
		return strings.TrimSpace(tenants[i].Name) < strings.TrimSpace(tenants[j].Name)
	})

	for _, tenant := range tenants {
		tenantName := strings.TrimSpace(tenant.Name)
		if tenantName == "" {
			continue
		}
		envs, err := store.ListEnvConfigs(tenantName)
		if err != nil {
			return nil, nil, err
		}
		sort.Slice(envs, func(i, j int) bool {
			return strings.TrimSpace(envs[i].Name) < strings.TrimSpace(envs[j].Name)
		})
		for _, env := range envs {
			environmentName := strings.TrimSpace(env.Name)
			if environmentName == "" {
				continue
			}
			ref := environmentPortRef{Key: EnvironmentPortKey(tenantName, environmentName), Env: env}
			if env.LocalPortRangeStart > 0 {
				persisted = append(persisted, ref)
			} else {
				unpersisted = append(unpersisted, ref)
			}
		}
	}
	return persisted, unpersisted, nil
}

// ResolveAllEnvironmentLocalPorts keeps persisted range starts, then fills the
// lowest free index in (tenant, env) order; duplicates are an overlap error.
func ResolveAllEnvironmentLocalPorts(store EnvironmentPortStore) (map[string]EnvironmentLocalPorts, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}

	persisted, unpersisted, err := CollectEnvironmentPortRefs(store)
	if err != nil {
		return nil, err
	}

	allocations := make(map[string]EnvironmentLocalPorts, len(persisted)+len(unpersisted))
	claimedByIndex := make(map[int]string, len(persisted)+len(unpersisted))

	if err := allocatePersistedEnvironmentPorts(persisted, allocations, claimedByIndex); err != nil {
		return nil, err
	}
	if err := allocateUnpersistedEnvironmentPorts(unpersisted, allocations, claimedByIndex); err != nil {
		return nil, err
	}

	return allocations, nil
}

func allocatePersistedEnvironmentPorts(persisted []environmentPortRef, allocations map[string]EnvironmentLocalPorts, claimedByIndex map[int]string) error {
	for _, ref := range persisted {
		index, err := EnvironmentPortIndexForRangeStart(ref.Env.LocalPortRangeStart, ref.Key)
		if err != nil {
			return err
		}
		if other, dup := claimedByIndex[index]; dup {
			return LocalPortRangeOverlapError{A: other, B: ref.Key, RangeStart: ref.Env.LocalPortRangeStart}
		}
		ports, err := environmentLocalPortsForIndex(index, ref.Env)
		if err != nil {
			return err
		}
		claimedByIndex[index] = ref.Key
		allocations[ref.Key] = ports
	}
	return nil
}

func allocateUnpersistedEnvironmentPorts(unpersisted []environmentPortRef, allocations map[string]EnvironmentLocalPorts, claimedByIndex map[int]string) error {
	walkerIndex := 0
	for _, ref := range unpersisted {
		for {
			if _, claimed := claimedByIndex[walkerIndex]; !claimed {
				break
			}
			walkerIndex++
		}
		ports, err := environmentLocalPortsForIndex(walkerIndex, ref.Env)
		if err != nil {
			return err
		}
		claimedByIndex[walkerIndex] = ref.Key
		allocations[ref.Key] = ports
		walkerIndex++
	}
	return nil
}

func ResolveEnvironmentLocalPorts(store EnvironmentPortStore, tenant, environment string) (EnvironmentLocalPorts, error) {
	allocations, err := ResolveAllEnvironmentLocalPorts(store)
	if err != nil {
		return EnvironmentLocalPorts{}, err
	}

	ports, ok := allocations[EnvironmentPortKey(tenant, environment)]
	if !ok {
		return EnvironmentLocalPorts{}, fmt.Errorf("local port range is not configured for %s/%s", strings.TrimSpace(tenant), strings.TrimSpace(environment))
	}
	return ports, nil
}

func EnvironmentLocalPortsForTarget(store OpenStore, tenant string, env EnvConfig) (EnvironmentLocalPorts, error) {
	portStore, ok := store.(EnvironmentPortStore)
	if !ok {
		return EnvironmentLocalPorts{}, fmt.Errorf("local port range cannot be resolved without tenant/environment listing for %s/%s", strings.TrimSpace(tenant), strings.TrimSpace(env.Name))
	}
	// Always resolve via the two-pass resolver so cross-tenant overlaps are caught.
	ports, err := ResolveEnvironmentLocalPorts(portStore, tenant, env.Name)
	if err != nil {
		return EnvironmentLocalPorts{}, err
	}
	if env.SSHD.LocalPort > 0 {
		ports.SSH = env.SSHD.LocalPort
	}
	return ports, nil
}

func environmentLocalPortsForIndex(index int, env EnvConfig) (EnvironmentLocalPorts, error) {
	if index < 0 {
		return EnvironmentLocalPorts{}, fmt.Errorf("environment index must be non-negative")
	}
	if MCPServicePortOffset >= EnvironmentPortRangeSize ||
		APIServicePortOffset >= EnvironmentPortRangeSize ||
		SSHServicePortOffset >= EnvironmentPortRangeSize ||
		ContributeAppServicePortOffset >= EnvironmentPortRangeSize {
		return EnvironmentLocalPorts{}, fmt.Errorf("service port offsets exceed environment local port range size")
	}

	rangeStart := LowerServicePort + index*EnvironmentPortRangeSize
	rangeEnd := rangeStart + EnvironmentPortRangeSize - 1
	if rangeEnd > 65535 {
		return EnvironmentLocalPorts{}, fmt.Errorf("local port range exceeds maximum TCP port for environment index %d", index)
	}

	ports := EnvironmentLocalPorts{
		RangeStart:    rangeStart,
		RangeEnd:      rangeEnd,
		MCP:           rangeStart + MCPServicePortOffset,
		API:           rangeStart + APIServicePortOffset,
		SSH:           rangeStart + SSHServicePortOffset,
		ContributeApp: rangeStart + ContributeAppServicePortOffset,
	}
	if env.SSHD.LocalPort > 0 {
		ports.SSH = env.SSHD.LocalPort
	}
	return ports, nil
}

func EnvironmentPortIndexForRangeStart(rangeStart int, ownerKey string) (int, error) {
	if rangeStart < LowerServicePort {
		return 0, fmt.Errorf("local port range start %d for %s is below %d", rangeStart, ownerKey, LowerServicePort)
	}
	offset := rangeStart - LowerServicePort
	if offset%EnvironmentPortRangeSize != 0 {
		return 0, fmt.Errorf("local port range start %d for %s is not aligned to %d-port boundaries from %d", rangeStart, ownerKey, EnvironmentPortRangeSize, LowerServicePort)
	}
	return offset / EnvironmentPortRangeSize, nil
}

func EnvironmentPortKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}
