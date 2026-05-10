package routes

import (
	"errors"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

func writeError(w http.ResponseWriter, status int, message string) {
	http.Error(w, message, status)
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
	default:
		writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}
}
