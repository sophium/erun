package eruncommon

func LinuxPackageBuildsSupported() bool {
	if DetectHost().OS != HostOSLinux {
		return false
	}
	_, err := hostLookPath("dpkg-deb")
	return err == nil
}
