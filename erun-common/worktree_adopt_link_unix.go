//go:build !windows

package eruncommon

import (
	"io/fs"
	"syscall"
)

// fileLinkKey identifies one inode, so a tree copy can recreate hard links
// between files instead of duplicating their content.
type fileLinkKey struct {
	dev uint64
	ino uint64
}

// fileLinkIdentity reports the inode a regular file shares with other names,
// and false for a file with a single name, which needs no link bookkeeping.
func fileLinkIdentity(info fs.FileInfo) (fileLinkKey, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink < 2 {
		return fileLinkKey{}, false
	}
	// Dev is int32 on darwin and uint64 on linux; the conversion serves both.
	return fileLinkKey{dev: uint64(stat.Dev), ino: stat.Ino}, true
}
