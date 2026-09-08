package eruncommon

import (
	"io"
	"os"
)

// PortForwardLogMaxBytes caps a port-forward's own kubectl log so a forward
// that stays healthy and reused for weeks cannot grow it without limit.
// Matches the cap the desktop app log and the per-env trace log already use
// for the same reason (erun-ui/app_log.go's appLogMaxBytes, envTraceLogMaxBytes
// above) -- one rotated backup keeps the pair under ~2x this, and 5MB holds
// tens of thousands of lines, far more than the handful of diagnostic lines
// (a stale-forward notice, a dropped-connection error) that actually matter
// when a forward misbehaves.
const PortForwardLogMaxBytes = 5 * 1024 * 1024

// RotateOversizedFile bounds a log file that a still-running process may hold
// open for writing, without that process reopening the file or even knowing
// rotation happened.
//
// kubectl port-forward's stdout/stderr fd is opened O_APPEND, so every write
// repositions to the file's *current* end-of-file at write time rather than an
// offset fixed when the fd was opened. That makes an in-place truncate safe:
// the next write from the still-running process lands at the new (zero)
// end-of-file instead of leaving a hole. Renaming the file instead -- the
// pattern used for logs this process itself reopens on its own next
// invocation (see EnvTraceLogPath's openEnvTraceLog, erun-ui's
// boundedLogFile) -- would not bound this file at all: the writer's fd
// follows the inode, not the path, so the "rotated" file would keep growing
// under its new name while the fresh file at the canonical path sat empty.
//
// Reports whether it rotated, so a caller that wants to say so (a trace
// line) can.
func RotateOversizedFile(path string, maxBytes int64) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Size() <= maxBytes {
		return false, nil
	}
	if err := copyFileContents(path, path+".1"); err != nil {
		return false, err
	}
	return true, os.Truncate(path, 0)
}

func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()
	_, err = io.Copy(out, in)
	return err
}
