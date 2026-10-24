package shared

import (
	"fmt"
	"os"
)

// ErunExecutablePath resolves this binary's own path, which is what supervises
// a started job. Resolving it here rather than looking "erun" up on PATH means a
// job is supervised by the same build that started it.
func ErunExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve the erun executable to supervise the job: %w", err)
	}
	return executable, nil
}
