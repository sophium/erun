package main

import (
	"os"

	"github.com/sophium/erun/cmd"
	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
)

func main() {
	if exitCode := run(); exitCode != 0 {
		os.Exit(exitCode)
	}
}

func run() int {
	if err := cmd.Execute(); err != nil {
		if !internal.IsReported(err) {
			logger := eruncommon.NewLogger(0)
			logger.Fatal(err)
		}
		return internal.ExitCodeFor(err)
	}
	return 0
}
