package routes

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// errorEnvelope is the {code, message, details} JSON body every error
// response now carries. code is always populated, even where no call site
// names a more specific business code, so a client branching on it never sees
// the field simply absent.
type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// nonAlphanumericRun matches any run of characters that don't belong in a
// SCREAMING_SNAKE_CASE code, so http.StatusText's punctuation (e.g. "I'm a
// teapot") collapses to a single separator instead of leaking through.
var nonAlphanumericRun = regexp.MustCompile(`[^A-Za-z0-9]+`)

// defaultErrorCode derives a machine code from the HTTP status alone. It is
// the fallback every error gets when the call site has no more specific code
// to report — e.g. NOT_FOUND for 404, INTERNAL_SERVER_ERROR for 500.
func defaultErrorCode(status int) string {
	text := http.StatusText(status)
	if text == "" {
		return "ERROR"
	}
	code := strings.Trim(nonAlphanumericRun.ReplaceAllString(text, "_"), "_")
	return strings.ToUpper(code)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeErrorCode(w, status, defaultErrorCode(status), message)
}

// writeErrorCode reports a specific machine-readable code instead of the
// status-derived default, for the business conditions the docs name one for.
func writeErrorCode(w http.ResponseWriter, status int, code, message string) {
	writeErrorDetails(w, status, code, message, nil)
}

// writeErrorDetails is writeErrorCode plus a details object, for the few
// codes whose docs promise one (e.g. INVALID_TRANSITION's validTargets).
func writeErrorDetails(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, errorEnvelope{Code: code, Message: message, Details: details})
}

func writeRepositoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, http.StatusText(http.StatusForbidden))
	case errors.Is(err, repository.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
	case errors.Is(err, repository.ErrMissingSecurityContext):
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, http.StatusText(http.StatusConflict))
	default:
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}
}
