//go:build windows

package eruncommon

import "io/fs"

// fileLinkKey identifies one inode; Windows exposes no inode through
// fs.FileInfo, so hard links there are copied as separate files.
type fileLinkKey struct{}

func fileLinkIdentity(fs.FileInfo) (fileLinkKey, bool) {
	return fileLinkKey{}, false
}
