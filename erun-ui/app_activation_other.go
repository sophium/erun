//go:build !darwin

package main

// observeAppActivation is a no-op off darwin.
func observeAppActivation() {}
