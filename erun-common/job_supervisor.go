package eruncommon

// FirstNonEmptyString returns the first non-empty value, or "" if all are.
func FirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
