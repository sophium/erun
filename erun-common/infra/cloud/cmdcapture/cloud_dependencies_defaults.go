package cmdcapture

import (
	"bytes"
	"io"
	"strings"
)

func CaptureWriter(writer io.Writer) (io.Writer, *bytes.Buffer) {
	buffer := new(bytes.Buffer)
	if writer == nil {
		return buffer, buffer
	}
	return io.MultiWriter(writer, buffer), buffer
}

func CommandErrorMessage(err error, stderr, fallback string) string {
	message := strings.TrimSpace(stderr)
	if message != "" {
		return message
	}
	if fallback != "" {
		return fallback + ": " + err.Error()
	}
	return err.Error()
}
