package eruncommon

const (
	LowerServicePort = 17000

	MCPServicePortOffset = 0
	MCPServicePort       = LowerServicePort + MCPServicePortOffset
)

func ServicePort(offset int) int {
	return LowerServicePort + offset
}
