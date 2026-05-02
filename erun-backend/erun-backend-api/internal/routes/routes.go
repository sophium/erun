package routes

import "net/http"

type ProtectedRouteRegistrar func(method string, apiPath string, handler http.Handler)
