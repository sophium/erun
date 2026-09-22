package routes

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// pathIDErrorCode is the machine code a malformed path id is reported under.
// A caller can tell it apart from the status-derived default a genuine server
// fault still carries, which is the whole point of not letting this class of
// failure reach writeRepositoryError's 500 default.
const pathIDErrorCode = "INVALID_PATH_ID"

// nonUUIDPathParams names the route path parameters that are deliberately not
// externally visible API ids, so WithUUIDPathIDs leaves them alone.
//
// The list is an exception list rather than an allow-list of ids on purpose:
// "Use UUIDv7 for all externally visible API IDs" makes "this segment is an
// id" the default for a `{...}` parameter, so a route added later is guarded
// without its author having to remember to opt in, and a parameter that
// genuinely is not an id has to say so here, with a reason.
var nonUUIDPathParams = map[string]bool{
	// The cloud provider credential's own alias, chosen by whoever registers
	// it -- an operator-facing label, not a row id.
	"alias": true,
	// The identity provider's own subject identifier for a user (a Zitadel
	// user id such as "123456789012345678"). This API passes it through to
	// the IdP rather than minting it, so it is not a UUID.
	"external_id": true,
}

// pathParamNames returns every "{name}" parameter of a canonical route
// pattern, in the order it appears. Patterns are registered as literals, so
// this runs once per route at registration rather than once per request.
func pathParamNames(apiPath string) []string {
	var names []string
	for _, segment := range strings.Split(apiPath, "/") {
		if len(segment) > 2 && strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			names = append(names, segment[1:len(segment)-1])
		}
	}
	return names
}

// WithUUIDPathIDs wraps a registered route's handler so that a path parameter
// naming an externally visible id is refused as a client error before the
// handler runs.
//
// Without it a syntactically malformed id travels as far as the database,
// which rejects it at parse time; the resulting driver error matches none of
// the repository's sentinels, so the route layer's generic fallback reports it
// as a server fault. That tells a caller the platform is broken when the truth
// is that they mistyped an id -- opposite remedies -- and it puts every such
// typo into the platform's own 5xx rate.
//
// The guard belongs here rather than in each handler or in the repository
// because path values and status codes are the routes layer's own adaptation
// (see AGENTS.md "Routes Layer"), and because binding it at registration
// covers every route that has an id segment -- including ones added later, and
// the ones whose id is not looked up by primary key but used as a filter,
// inserted, or handed to a service.
//
// It runs inside the authentication wrapper, so an unauthenticated caller
// still gets 401 for every id shape and the parse behaviour stays
// unobservable until the caller is authorized.
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

// isCanonicalUUID reports whether value is a UUID written in the hyphenated
// 8-4-4-4-12 form, in either case, that every id this API mints or accepts
// uses.
//
// The version is deliberately not checked. A well-formed id that names nothing
// is a 404, not a malformed one, and the canonical absent id callers test the
// not-found path with -- 00000000-0000-0000-0000-000000000000 -- is not a v7,
// so requiring v7 here would turn a correct 404 into a 400. Only the spelling
// is judged. uuid.Parse alone is too permissive to judge it: it also accepts
// brace-, urn:- and unhyphenated spellings, which are shapes this API never
// emits and which the database reads differently, so the parsed value is
// required to render back to the text that was given.
func isCanonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.String(), value)
}
