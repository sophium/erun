package eruncommon

import (
	"fmt"
	"sort"
	"strings"
)

const (
	LowerServicePort         = 17000
	EnvironmentPortRangeSize = 100

	MCPServicePortOffset = 0
	SSHServicePortOffset = 22
	APIServicePortOffset = 33

	MCPServicePort      = LowerServicePort + MCPServicePortOffset
	APIServicePort      = LowerServicePort + APIServicePortOffset
	DefaultSSHLocalPort = LowerServicePort + SSHServicePortOffset
)

type EnvironmentLocalPorts struct {
	RangeStart int `json:"rangeStart,omitempty"`
	RangeEnd   int `json:"rangeEnd,omitempty"`
	MCP        int `json:"mcp,omitempty"`
	API        int `json:"api,omitempty"`
	SSH        int `json:"ssh,omitempty"`
}

type environmentPortStore interface {
	ListTenantConfigs() ([]TenantConfig, error)
	ListEnvConfigs(string) ([]EnvConfig, error)
}

// ErrLocalPortRangeOverlap is returned when two envs persist the same
// LocalPortRangeStart. Surfacing the conflict is preferred over silent
// reassignment because the persisted value is the durable user-visible
// contract and either of the two configs must be edited to resolve it.
type ErrLocalPortRangeOverlap struct {
	A          string
	B          string
	RangeStart int
}

func (e ErrLocalPortRangeOverlap) Error() string {
	return fmt.Sprintf("local port range start %d is claimed by both %s and %s; edit localportrangestart in one of the env configs to resolve", e.RangeStart, e.A, e.B)
}

func ServicePort(offset int) int {
	return LowerServicePort + offset
}

// EnvironmentLocalPortsFromRangeStart derives the per-service ports from a
// persisted range start. Callers should prefer this over assembling the
// struct piecemeal so the offsets stay in one place.
func EnvironmentLocalPortsFromRangeStart(rangeStart int) EnvironmentLocalPorts {
	if rangeStart <= 0 {
		return EnvironmentLocalPorts{}
	}
	return EnvironmentLocalPorts{
		RangeStart: rangeStart,
		RangeEnd:   rangeStart + EnvironmentPortRangeSize - 1,
		MCP:        rangeStart + MCPServicePortOffset,
		API:        rangeStart + APIServicePortOffset,
		SSH:        rangeStart + SSHServicePortOffset,
	}
}

// LocalPortsForResult derives the effective local ports for an OpenResult.
// It prefers the persisted EnvConfig.LocalPortRangeStart over the resolver's
// in-memory allocation so the result is stable regardless of how many other
// envs exist. If neither is set it returns a zero value — callers that
// actually need to bind a port will fail loudly on port 0, which is the
// intended signal: a missing range is a bug upstream, not a default.
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
	}
	if result.EnvConfig.SSHD.LocalPort > 0 {
		ports.SSH = result.EnvConfig.SSHD.LocalPort
	}
	return ports
}

func MCPPortForResult(result OpenResult) int {
	return LocalPortsForResult(result).MCP
}

func APIPortForResult(result OpenResult) int {
	return LocalPortsForResult(result).API
}

func SSHLocalPortForResult(result OpenResult) int {
	return LocalPortsForResult(result).SSH
}

// ResolveAllEnvironmentLocalPorts returns a per-env port allocation in two
// passes. Pass A locks in any env whose config has a non-zero
// LocalPortRangeStart at its declared range. Pass B walks the remaining envs
// in alphabetical (tenant, env) order, picking the lowest index not already
// claimed by Pass A. Two persisted envs that share a range start cause an
// ErrLocalPortRangeOverlap so the misconfiguration is surfaced instead of
// silently reassigned.
func ResolveAllEnvironmentLocalPorts(store environmentPortStore) (map[string]EnvironmentLocalPorts, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return nil, err
	}
	sort.Slice(tenants, func(i, j int) bool {
		return strings.TrimSpace(tenants[i].Name) < strings.TrimSpace(tenants[j].Name)
	})

	type envRef struct {
		key string
		env EnvConfig
	}
	var persisted, unpersisted []envRef
	for _, tenant := range tenants {
		tenantName := strings.TrimSpace(tenant.Name)
		if tenantName == "" {
			continue
		}
		envs, err := store.ListEnvConfigs(tenantName)
		if err != nil {
			return nil, err
		}
		sort.Slice(envs, func(i, j int) bool {
			return strings.TrimSpace(envs[i].Name) < strings.TrimSpace(envs[j].Name)
		})
		for _, env := range envs {
			environmentName := strings.TrimSpace(env.Name)
			if environmentName == "" {
				continue
			}
			ref := envRef{key: environmentPortKey(tenantName, environmentName), env: env}
			if env.LocalPortRangeStart > 0 {
				persisted = append(persisted, ref)
			} else {
				unpersisted = append(unpersisted, ref)
			}
		}
	}

	allocations := make(map[string]EnvironmentLocalPorts, len(persisted)+len(unpersisted))
	claimedByIndex := make(map[int]string, len(persisted)+len(unpersisted))

	for _, ref := range persisted {
		index, err := environmentPortIndexForRangeStart(ref.env.LocalPortRangeStart, ref.key)
		if err != nil {
			return nil, err
		}
		if other, dup := claimedByIndex[index]; dup {
			return nil, ErrLocalPortRangeOverlap{A: other, B: ref.key, RangeStart: ref.env.LocalPortRangeStart}
		}
		ports, err := environmentLocalPortsForIndex(index, ref.env)
		if err != nil {
			return nil, err
		}
		claimedByIndex[index] = ref.key
		allocations[ref.key] = ports
	}

	walkerIndex := 0
	for _, ref := range unpersisted {
		for {
			if _, claimed := claimedByIndex[walkerIndex]; !claimed {
				break
			}
			walkerIndex++
		}
		ports, err := environmentLocalPortsForIndex(walkerIndex, ref.env)
		if err != nil {
			return nil, err
		}
		claimedByIndex[walkerIndex] = ref.key
		allocations[ref.key] = ports
		walkerIndex++
	}

	return allocations, nil
}

func ResolveEnvironmentLocalPorts(store environmentPortStore, tenant, environment string) (EnvironmentLocalPorts, error) {
	allocations, err := ResolveAllEnvironmentLocalPorts(store)
	if err != nil {
		return EnvironmentLocalPorts{}, err
	}

	ports, ok := allocations[environmentPortKey(tenant, environment)]
	if !ok {
		return EnvironmentLocalPorts{}, fmt.Errorf("local port range is not configured for %s/%s", strings.TrimSpace(tenant), strings.TrimSpace(environment))
	}
	return ports, nil
}

func environmentLocalPortsForTarget(store OpenStore, tenant string, env EnvConfig) (EnvironmentLocalPorts, error) {
	portStore, ok := store.(environmentPortStore)
	if !ok {
		return EnvironmentLocalPorts{}, fmt.Errorf("local port range cannot be resolved without tenant/environment listing for %s/%s", strings.TrimSpace(tenant), strings.TrimSpace(env.Name))
	}
	// Always go through the two-pass resolver, even when env already has a
	// persisted range start: the resolver is what catches cross-tenant
	// overlap between two persisted envs, and skipping it for the
	// already-persisted env would let the conflict slip through silently.
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
	if MCPServicePortOffset >= EnvironmentPortRangeSize || APIServicePortOffset >= EnvironmentPortRangeSize || SSHServicePortOffset >= EnvironmentPortRangeSize {
		return EnvironmentLocalPorts{}, fmt.Errorf("service port offsets exceed environment local port range size")
	}

	rangeStart := LowerServicePort + index*EnvironmentPortRangeSize
	rangeEnd := rangeStart + EnvironmentPortRangeSize - 1
	if rangeEnd > 65535 {
		return EnvironmentLocalPorts{}, fmt.Errorf("local port range exceeds maximum TCP port for environment index %d", index)
	}

	ports := EnvironmentLocalPorts{
		RangeStart: rangeStart,
		RangeEnd:   rangeEnd,
		MCP:        rangeStart + MCPServicePortOffset,
		API:        rangeStart + APIServicePortOffset,
		SSH:        rangeStart + SSHServicePortOffset,
	}
	if env.SSHD.LocalPort > 0 {
		ports.SSH = env.SSHD.LocalPort
	}
	return ports, nil
}

func environmentPortIndexForRangeStart(rangeStart int, ownerKey string) (int, error) {
	if rangeStart < LowerServicePort {
		return 0, fmt.Errorf("local port range start %d for %s is below %d", rangeStart, ownerKey, LowerServicePort)
	}
	offset := rangeStart - LowerServicePort
	if offset%EnvironmentPortRangeSize != 0 {
		return 0, fmt.Errorf("local port range start %d for %s is not aligned to %d-port boundaries from %d", rangeStart, ownerKey, EnvironmentPortRangeSize, LowerServicePort)
	}
	return offset / EnvironmentPortRangeSize, nil
}

func environmentPortKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}
