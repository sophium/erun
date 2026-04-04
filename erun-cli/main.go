package main

import (
	"os"

	"github.com/sophium/erun/cmd"
	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
)

var runCLI = cmd.Execute

func main() {
	if exitCode := run(); exitCode != 0 {
		os.Exit(exitCode)
	}
}

func run() int {
	if err := runCLI(); err != nil {
		if !internal.IsReported(err) {
			logger := eruncommon.NewLogger(0)
			logger.Fatal(err)
		}
		return 1
	}
	return 0
}
