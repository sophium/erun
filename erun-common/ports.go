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

func ServicePort(offset int) int {
	return LowerServicePort + offset
}

func DefaultEnvironmentLocalPorts() EnvironmentLocalPorts {
	ports, _ := environmentLocalPortsForIndex(0, EnvConfig{})
	return ports
}

func LocalPortsForResult(result OpenResult) EnvironmentLocalPorts {
	ports := result.LocalPorts
	if ports.RangeStart == 0 || ports.RangeEnd == 0 {
		ports = DefaultEnvironmentLocalPorts()
	}
	if ports.MCP == 0 {
		ports.MCP = ports.RangeStart + MCPServicePortOffset
	}
	if ports.API == 0 {
		ports.API = ports.RangeStart + APIServicePortOffset
	}
	if ports.SSH == 0 {
		ports.SSH = ports.RangeStart + SSHServicePortOffset
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

	allocations := make(map[string]EnvironmentLocalPorts)
	index := 0
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

			ports, err := environmentLocalPortsForIndex(index, env)
			if err != nil {
				return nil, err
			}
			allocations[environmentPortKey(tenantName, environmentName)] = ports
			index++
		}
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
	if portStore, ok := store.(environmentPortStore); ok {
		ports, err := ResolveEnvironmentLocalPorts(portStore, tenant, env.Name)
		if err != nil {
			return EnvironmentLocalPorts{}, err
		}
		if env.SSHD.LocalPort > 0 {
			ports.SSH = env.SSHD.LocalPort
		}
		return ports, nil
	}

	ports := DefaultEnvironmentLocalPorts()
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

func environmentPortKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}
