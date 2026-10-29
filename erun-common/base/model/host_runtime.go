package model

type HostOS string

const (
	HostOSDarwin  HostOS = "darwin"
	HostOSLinux   HostOS = "linux"
	HostOSWindows HostOS = "windows"
	HostOSUnknown HostOS = "unknown"
)
