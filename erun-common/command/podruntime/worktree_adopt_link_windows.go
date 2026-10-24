//go:build windows

package podruntime

import (
	"io/fs"
)

// fs.FileInfo exposes no inode on Windows, so hard links copy as separate files.
type fileLinkKey struct{}

func fileLinkIdentity(fs.FileInfo) (fileLinkKey, bool) {
	return fileLinkKey{}, false
}
