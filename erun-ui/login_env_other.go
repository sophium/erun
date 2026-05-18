//go:build !darwin

package main

// importLoginShellEnv is a no-op outside macOS. Linux desktop GUIs
// typically inherit the user session's env from the display manager
// (gdm/sddm/etc) and the user shell's profile-d scripts, and Windows
// uses its own PATH machinery. See darwin_login_env.go for the macOS
// implementation and the reason this exists.
func importLoginShellEnv() {}
