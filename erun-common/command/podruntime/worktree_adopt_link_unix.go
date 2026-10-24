//go:build !windows

package podruntime

import (
	"io/fs"
	"syscall"
)

type fileLinkKey struct {
	dev uint64
	ino uint64
}

// fileLinkIdentity returns false for a single-name file.
func fileLinkIdentity(info fs.FileInfo) (fileLinkKey, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink < 2 {
		return fileLinkKey{}, false
	}
	return fileLinkKey{dev: deviceNumber(stat.Dev), ino: stat.Ino}, true
}

// deviceNumber widens Stat_t.Dev, which is int32 on darwin and uint64 on linux.
func deviceNumber[T ~int32 | ~uint32 | ~int64 | ~uint64](dev T) uint64 {
	return uint64(dev)
}
