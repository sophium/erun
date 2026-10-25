package runtimepod

// MCPTokenMinter runs before every request so long calls never carry an expired token.
type MCPTokenMinter func() (string, error)
