//go:build !darwin

package main

// importLoginShellEnv is a no-op off macOS: Linux and Windows GUI apps
// already inherit the user's login environment, so there is nothing to import.
func importLoginShellEnv() {}
