package routes

import (
	"encoding/json"
	"net/http"
)

func decodeJSON(r *http.Request, target any) error {
	defer func() { _ = r.Body.Close() }()
	return json.NewDecoder(r.Body).Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// The status is already sent, so a failed body write has no one left to report to.
		return
	}
}
