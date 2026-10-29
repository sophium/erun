package config

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
