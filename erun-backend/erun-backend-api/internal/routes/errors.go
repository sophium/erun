package routes

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// errorEnvelope is the JSON body of every error response; code is always set.
type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

var nonAlphanumericRun = regexp.MustCompile(`[^A-Za-z0-9]+`)

// defaultErrorCode derives a machine code from the HTTP status alone.
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

// writeErrorCode reports a specific machine code instead of the status default.
func writeErrorCode(w http.ResponseWriter, status int, code, message string) {
	writeErrorDetails(w, status, code, message, nil)
}

// writeErrorDetails is writeErrorCode plus a details object.
func writeErrorDetails(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, errorEnvelope{Code: code, Message: message, Details: details})
}

func writeRepositoryError(w http.ResponseWriter, req *http.Request, err error) {
	var unresolvableMapping *repository.UnresolvableIssuerMappingError
	switch {
	case errors.As(err, &unresolvableMapping):
		// A conflict with the issuer registry's org-scoping mode, not a malformed request.
		writeErrorCode(w, http.StatusConflict, "UNRESOLVABLE_ISSUER_MAPPING", unresolvableMapping.Error())
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
	case errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, http.StatusText(http.StatusForbidden))
	case errors.Is(err, repository.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
	case errors.Is(err, repository.ErrMissingSecurityContext):
		logServerError(req, err)
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, http.StatusText(http.StatusConflict))
	default:
		logServerError(req, err)
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}
}

// logServerError logs route, actor, and the real error for every 5xx; the
// response keeps the generic text.
func logServerError(req *http.Request, err error) {
	tenantID, userID := "", ""
	if securityContext, ok := security.FromContext(req.Context()); ok {
		tenantID, userID = securityContext.TenantID, securityContext.ErunUserID
	}
	log.Printf("erun api server error method=%s path=%s tenant=%q user=%q error=%q", req.Method, req.URL.Path, tenantID, userID, err.Error())
}

// writeInternalError logs err in full and returns only message to the client.
func writeInternalError(w http.ResponseWriter, req *http.Request, message string, err error) {
	logServerError(req, err)
	writeError(w, http.StatusInternalServerError, message)
}

// logServerErrorForRoute is logServerError for call sites with only a context.
func logServerErrorForRoute(ctx context.Context, route string, err error) {
	tenantID, userID := "", ""
	if securityContext, ok := security.FromContext(ctx); ok {
		tenantID, userID = securityContext.TenantID, securityContext.ErunUserID
	}
	log.Printf("erun api server error route=%q tenant=%q user=%q error=%q", route, tenantID, userID, err.Error())
}
