package security

import "strings"

// BearerToken returns the token of an Authorization header that is exactly two
// whitespace-separated fields, the first "bearer" in any case. The header is read
// before anything is trusted and can be as large as the server admits, so the
// scan stops at a third field instead of collecting every one.
func BearerToken(header string) (string, bool) {
	var fields [2]string
	count := 0
	for field := range strings.FieldsSeq(header) {
		if count == len(fields) {
			return "", false
		}
		fields[count] = field
		count++
	}
	if count != len(fields) || !strings.EqualFold(fields[0], "bearer") {
		return "", false
	}
	return fields[1], true
}
