//go:build windows

package cmd

// findLocalPortHolder is a unix-only feature; on Windows we return "no
// holder identified" and let the caller fall through to the legacy
// "already in use" error path. Adoption of foreign port-forwards is not
// implemented for Windows.
func findLocalPortHolder(int) (int, []string, bool) {
	return 0, nil, false
}
