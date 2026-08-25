//go:build !darwin

package main

// observeAppActivation is darwin-only; elsewhere there is no AppKit
// frontmost-activation notification to observe.
func observeAppActivation() {}
