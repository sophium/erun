package main

import (
	"testing"

	"github.com/sophium/erun/cmd"
)

func TestMainInvokesCLI(t *testing.T) {
	called := false
	runCLI = func() error {
		called = true
		return nil
	}
	t.Cleanup(func() {
		runCLI = cmd.Execute
	})

	main()

	if !called {
		t.Fatalf("expected CLI to run")
	}
}
