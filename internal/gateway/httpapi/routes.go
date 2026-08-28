package httpapi

import "net/http"

// Route describes one canonical HTTP operation. It is the single routing
// catalog consumed by the live router and OpenAPI conformance tests.
type Route struct {
	Method          string
	Path            string
	OperationID     string
	Protected       bool
	RequestBody     bool
	QueryParameters []string
}

var routeCatalog = []Route{
	{Method: http.MethodGet, Path: "/healthz", OperationID: "getHealth"},
	{Method: http.MethodGet, Path: "/readyz", OperationID: "getReadiness"},
	{Method: http.MethodPost, Path: "/api/v1/auth/login", OperationID: "login", RequestBody: true},
	{Method: http.MethodPost, Path: "/api/v1/auth/logout", OperationID: "logout", Protected: true},
	{Method: http.MethodGet, Path: "/api/v1/auth/session", OperationID: "getSession", Protected: true},
	{Method: http.MethodGet, Path: "/api/v1/state", OperationID: "getState", Protected: true},
	{Method: http.MethodPost, Path: "/api/v1/state", OperationID: "saveState", Protected: true, RequestBody: true},
	{Method: http.MethodGet, Path: "/api/v1/profiles", OperationID: "listProfiles", Protected: true},
	{Method: http.MethodGet, Path: "/api/v1/history", OperationID: "listHistory", Protected: true, QueryParameters: []string{"cursor", "limit"}},
	{Method: http.MethodGet, Path: "/api/v1/stats", OperationID: "getStats", Protected: true},
	{Method: http.MethodDelete, Path: "/api/v1/history/{historyID}", OperationID: "deleteHistory", Protected: true},
	{Method: http.MethodDelete, Path: "/api/v1/history", OperationID: "clearHistory", Protected: true},
	{Method: http.MethodGet, Path: "/api/v1/profiles/bundle", OperationID: "exportProfileBundle", Protected: true},
	{Method: http.MethodPut, Path: "/api/v1/profiles/bundle", OperationID: "importProfileBundle", Protected: true, RequestBody: true},
	{Method: http.MethodPut, Path: "/api/v1/profiles/{profileID}", OperationID: "saveProfile", Protected: true, RequestBody: true},
	{Method: http.MethodDelete, Path: "/api/v1/profiles/{profileID}", OperationID: "deleteProfile", Protected: true},
	{Method: http.MethodPost, Path: "/api/v1/profiles/{profileID}/models:refresh", OperationID: "refreshProfileModels", Protected: true},
	{Method: http.MethodPost, Path: "/api/v1/run", OperationID: "run", Protected: true, RequestBody: true},
	{Method: http.MethodGet, Path: "/api/v1/traces/{traceID}", OperationID: "getTrace", Protected: true},
	{Method: http.MethodGet, Path: "/api/v1/traces/{traceID}/artifacts/{artifactID}", OperationID: "getArtifact", Protected: true},
}

// Routes returns a copy of the canonical route catalog.
func Routes() []Route {
	routes := append([]Route(nil), routeCatalog...)
	for index := range routes {
		routes[index].QueryParameters = append([]string(nil), routes[index].QueryParameters...)
	}
	return routes
}
