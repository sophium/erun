package main

import (
	"fmt"
	"os"
	"strings"
)

// The environment readers live apart from main.go because both the API server
// and the oidc-bootstrap subcommand use them, and buildinfo_ldflags_test.go
// compiles this package with main.go replaced by a stub.

func envOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func intEnvOrDefault(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
