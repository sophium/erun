package kubeapi

import (
	"io"
)

type OpenKubectlRunnerFunc func(args []string, stdout, stderr io.Writer) error
