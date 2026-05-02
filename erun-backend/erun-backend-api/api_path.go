package backendapi

import (
	"context"
	"net/http"
)

type apiPathContextKey struct{}

func APIPathFromContext(ctx context.Context) (string, bool) {
	apiPath, ok := ctx.Value(apiPathContextKey{}).(string)
	return apiPath, ok
}

func withAPIPath(apiPath string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), apiPathContextKey{}, apiPath)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
