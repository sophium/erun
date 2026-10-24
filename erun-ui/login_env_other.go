//go:build !darwin

package main

// GUI apps off macOS already inherit the login environment.
func importLoginShellEnv() {}
