package model

// RouteCheckParams is `erun exec route-check`'s input.
type RouteCheckParams struct {
	// RoutesDir defaults to the checkout's erun-backend-api/internal/routes.
	RoutesDir string
}

// RouteProbeResult is one route's probe outcome.
type RouteProbeResult struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	ProbePath  string `json:"probePath"`
	StatusCode int    `json:"statusCode,omitempty"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
}

// RouteCheckResult is the full route-liveness report.
type RouteCheckResult struct {
	APIURL string `json:"apiUrl"`
	// PlaneReachable gates probing on GET /v1/whoami, so a down plane is not
	// misreported as every route missing.
	PlaneReachable    bool               `json:"planeReachable"`
	UnreachableReason string             `json:"unreachableReason,omitempty"`
	RoutesChecked     int                `json:"routesChecked"`
	Routes            []RouteProbeResult `json:"routes,omitempty"`
	Missing           []RouteProbeResult `json:"missing,omitempty"`
	Errors            []RouteProbeResult `json:"errors,omitempty"`
}
