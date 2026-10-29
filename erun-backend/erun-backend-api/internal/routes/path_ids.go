package routes

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// pathIDErrorCode marks a malformed path id, distinct from a server fault's code.
const pathIDErrorCode = "INVALID_PATH_ID"

// nonUUIDPathParams is an exception list so a new `{...}` parameter is guarded
// by default; a non-id parameter must be named here.
var nonUUIDPathParams = map[string]bool{
	// Operator-chosen label, not a row id.
	"alias": true,
	// The IdP's own subject id, passed through rather than minted.
	"external_id": true,
}

func pathParamNames(apiPath string) []string {
	var names []string
	for _, segment := range strings.Split(apiPath, "/") {
		if len(segment) > 2 && strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			names = append(names, segment[1:len(segment)-1])
		}
	}
	return names
}

// WithUUIDPathIDs refuses a malformed path id with 400 before the handler runs;
// otherwise the database parse error surfaces as a 500. It runs inside the auth
// wrapper, so unauthenticated callers still get 401 for every id shape.
func WithUUIDPathIDs(apiPath string, handler http.Handler) http.Handler {
	var idParams []string
	for _, name := range pathParamNames(apiPath) {
		if !nonUUIDPathParams[name] {
			idParams = append(idParams, name)
		}
	}
	if len(idParams) == 0 {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for _, name := range idParams {
			value := req.PathValue(name)
			if isCanonicalUUID(value) {
				continue
			}
			writeErrorCode(w, http.StatusBadRequest, pathIDErrorCode,
				fmt.Sprintf("path parameter %q must be a UUID such as 01a01b39-0000-7000-8000-000000000000; got %q", name, value))
			return
		}
		handler.ServeHTTP(w, req)
	})
}

// isCanonicalUUID requires the hyphenated 8-4-4-4-12 spelling but not v7, so the
// all-zero id still reaches a 404. uuid.Parse alone also accepts brace, urn: and
// unhyphenated forms, hence the round-trip check.
func isCanonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.String(), value)
}
