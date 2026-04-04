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
