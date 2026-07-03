//go:build windows

package cmd

// findLocalPortHolder has no Windows implementation: adopting foreign port-forwards is a unix-only feature.
func findLocalPortHolder(int) (int, []string, bool) {
	return 0, nil, false
}
