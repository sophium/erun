package internal

import "errors"

type reportedError struct {
	err error
}

func (e reportedError) Error() string {
	return e.err.Error()
}

func (e reportedError) Unwrap() error {
	return e.err
}

func MarkReported(err error) error {
	if err == nil {
		return nil
	}
	return reportedError{err: err}
}

func IsReported(err error) bool {
	var reported reportedError
	return errors.As(err, &reported)
}

// exitCodeError carries the process exit code a failure should produce. Most
// failures are just "it did not work" and exit 1; a few carry an outcome a
// caller has to branch on — a bounded wait elapsing is not the same event as the
// work failing — and collapsing those onto one code is what makes a shell caller
// guess.
type exitCodeError struct {
	err  error
	code int
}

func (e exitCodeError) Error() string {
	return e.err.Error()
}

func (e exitCodeError) Unwrap() error {
	return e.err
}

// WithExitCode tags err with the exit code the process should end on.
func WithExitCode(err error, code int) error {
	if err == nil || code == 0 {
		return err
	}
	return exitCodeError{err: err, code: code}
}

// ExitCodeFor reports the exit code err asks for, defaulting to 1.
func ExitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var coded exitCodeError
	if errors.As(err, &coded) {
		return coded.code
	}
	return 1
}
