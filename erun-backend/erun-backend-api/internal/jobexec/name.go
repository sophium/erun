package jobexec

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// MaxJobNameLength is the Kubernetes object name limit.
const MaxJobNameLength = 63

const shortIDLength = 8

// SanitizeName maps s to a DNS-safe Job name component.
func SanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// ShortID hashes an attempt id into a Job-name suffix. Slicing would collide:
// UUIDv7 prefixes are timestamps.
func ShortID(id string) string {
	digest := fnv.New32a()
	_, _ = digest.Write([]byte(id))
	return fmt.Sprintf("%0*x", shortIDLength, digest.Sum32())
}
