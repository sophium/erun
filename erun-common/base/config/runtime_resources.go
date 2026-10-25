package config

// DefaultLimitRangeDefaultRequestCPU/Memory are small fixed requests: a
// defaultRequest equal to the quota would turn the quota into a minimum node size.
const (
	DefaultLimitRangeDefaultRequestCPU    = "0.25"
	DefaultLimitRangeDefaultRequestMemory = "1024Mi"
)
