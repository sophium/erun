package eruncommon

import (
	"os"
	"path/filepath"
)

// OpenBoundedAppendLog opens path for appending after rolling an existing log
// that has outgrown maxBytes aside to path+".1". That is what keeps a
// long-lived append-only log from growing without bound: a log under the cap is
// left exactly as it is, so the run an operator opens the file to read is still
// there, and only a log that already exceeded the cap is rotated. One
// generation is kept, so the pair stays under roughly twice maxBytes.
//
// Rotation is best-effort. A writer can still hold the file -- notably on
// Windows, where renaming an open file fails -- and a log that could not be
// rolled aside is far preferable to failing the command that only wants to
// append to it; the next start retries.
func OpenBoundedAppendLog(path string, maxBytes int64, perm os.FileMode) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	RotateOversizedLog(path, maxBytes)
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm)
}

// RotateOversizedLog replaces path+".1" with path when path has outgrown
// maxBytes, so the live file the next writer appends to starts empty. It
// reports whether it actually rotated, which a caller re-checking a
// long-lived writer's log -- rather than opening it -- needs in order to say
// so; OpenBoundedAppendLog ignores the result because the open that follows is
// what it is there for.
//
// Exported for the callers that hold an already-open file: a writer that is
// deliberately reused (a healthy port-forward, say) keeps appending to the
// file it opened at start, so the open-time rotation never runs again for as
// long as it lives, and this is the only way to re-apply the cap.
func RotateOversizedLog(path string, maxBytes int64) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxBytes {
		return false
	}
	_ = os.Rename(path, path+".1")
	return true
}
