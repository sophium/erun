package jobexec

import "strings"

// MaxJobNameLength is the cap Kubernetes puts on an object name. A caller
// composing a descriptive Job name trims against it.
const MaxJobNameLength = 63

// shortIDLength keeps enough of a random attempt id to separate concurrent and
// successive attempts without crowding out the readable part of a Job name.
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

// ShortID trims an attempt id down to the suffix that keeps two attempts on the
// same work apart.
func ShortID(id string) string {
	short := SanitizeName(id)
	if len(short) > shortIDLength {
		short = short[:shortIDLength]
	}
	return short
}
