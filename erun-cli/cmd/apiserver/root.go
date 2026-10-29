package apiserver

import (
	"io"
)

type (
	APILauncher func(io.Reader, io.Writer, io.Writer, []string) error
)
