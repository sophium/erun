package core

import (
	"os"
	"path/filepath"
)

// OpenBoundedAppendLog opens path for appending, first rolling a log over
// maxBytes aside to path+".1". Rotation is best-effort (Windows cannot rename an
// open file); appending matters more.
func OpenBoundedAppendLog(path string, maxBytes int64, perm os.FileMode) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	RotateOversizedLog(path, maxBytes)
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm)
}

// RotateOversizedLog moves path to path+".1" when it exceeds maxBytes and reports
// whether it did. It lets long-lived writers that never reopen re-apply the cap.
func RotateOversizedLog(path string, maxBytes int64) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxBytes {
		return false
	}
	_ = os.Rename(path, path+".1")
	return true
}
