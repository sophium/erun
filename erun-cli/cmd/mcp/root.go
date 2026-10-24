package mcp

import (
	"io"
)

type (
	MCPLauncher func(io.Reader, io.Writer, io.Writer, []string) error
)
