package platformclient

// route_check.go implements `erun exec route-check`: proving every route
// erun-backend-api's router registers is actually reachable on a deployed
// plane, rather than trusting that "merged" means "deployed". A route can
// merge, be unit-tested, and close its issue while still 404ing on the live
// control plane for months, because nothing but a human running the CLI by
// hand had ever exercised the deployed route.
//
// The route inventory comes from DiscoverAPIRoutes (api_route_inventory.go),
// never a hand-maintained list -- a hand-maintained list drifts, which is
// the same defect one level up.
//
// Every probe is a plain GET, regardless of a route's own registered
// method: Go's net/http.ServeMux (1.22+) reports 405 Method Not Allowed for
// a path that matches some registered pattern under a different method, and
// erun-backend-api's NewHandler returns its mux directly with no wrapping
// NotFoundHandler -- so a GET against any registered route, whatever its
// real method, either reaches real logic (2xx/401/403/405/...) or gets Go's
// own literal, un-customized "404 page not found" body, and only that exact
// text means the route was never registered at all (an application-level
// 404 -- a well-formed request for an id that does not exist -- always
// returns erun-backend-api's own JSON error shape instead). Never sending a
// route's real method is what keeps this check from ever creating,
// updating, or deleting anything on a live plane.

// MuxDefaultNotFoundBody is the literal, unmodified text Go's net/http
// package writes for a request no registered pattern matches (http.NotFound
// -> http.Error); erun-backend-api never installs a custom NotFoundHandler,
// so this exact text is what tells "no route matched at all" apart from a
// route's own application-level 404. platform_client_http.go's
// PlatformStatusError.routeNotRegistered reuses this same constant so an
// ordinary command (not just route-check) can tell the two apart too.
const MuxDefaultNotFoundBody = "404 page not found"
