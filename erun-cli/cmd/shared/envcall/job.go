package envcall

import (
	"fmt"
	"os"
)

// ErunExecutablePath resolves this binary's path so a job is supervised by the build that started it.
func ErunExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve the erun executable to supervise the job: %w", err)
	}
	return executable, nil
}
