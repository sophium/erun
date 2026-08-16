package jobexec

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// MaxJobNameLength is the cap Kubernetes puts on an object name. A caller
// composing a descriptive Job name trims against it.
const MaxJobNameLength = 63

// shortIDLength keeps the Job-name suffix short enough not to crowd out the
// readable part of the name.
const shortIDLength = 8

// SanitizeName lowercases and replaces every character outside [a-z0-9-] so the
// result is a DNS-safe Job name component (versions carry dots, branches carry
// slashes, and so on).
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

// ShortID condenses an attempt id into the Job-name suffix that keeps two
// attempts on the same work apart. It hashes the whole id rather than slicing
// it: a UUIDv7's leading characters are its timestamp, so two ids minted
// milliseconds apart share them — and two attempts that collide onto one Job
// name means the second re-watches the first instead of running.
func ShortID(id string) string {
	digest := fnv.New32a()
	_, _ = digest.Write([]byte(id))
	return fmt.Sprintf("%0*x", shortIDLength, digest.Sum32())
}
