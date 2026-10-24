package platformclient

// route-check probes every route from DiscoverAPIRoutes with a plain GET, so it
// never mutates a live plane; only the mux's default 404 body means unregistered.

// MuxDefaultNotFoundBody is net/http's default 404 body. The backend installs no
// custom NotFoundHandler, so it distinguishes an unregistered route from an app 404.
const MuxDefaultNotFoundBody = "404 page not found"
