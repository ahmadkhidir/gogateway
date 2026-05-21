// Package router matches incoming HTTP requests to configured routes.
//
// Matching considers the request method, Host header, and URL path.
// Path patterns support a trailing wildcard: "/api/*" matches any path
// starting with "/api/".
package router

import (
	"net/http"
	"strings"

	"github.com/ahmadkhidir/gogateway/internal/config"
)

// Router holds the configured route table and provides matching.
type Router struct {
	routes []config.Route
}

// New creates a Router from the given route list. Routes are evaluated in
// declaration order; the first match wins.
func New(routes []config.Route) *Router {
	return &Router{routes: routes}
}

// Match finds the first route whose method, host, and path constraints
// match the incoming request. Returns nil when no route matches.
func (r *Router) Match(req *http.Request) *config.Route {
	for i := range r.routes {
		if matchRoute(&r.routes[i], req) {
			return &r.routes[i]
		}
	}
	return nil
}

// matchRoute checks whether a single route matches the request.
func matchRoute(route *config.Route, req *http.Request) bool {
	// Method check: if the route specifies methods the request must be one.
	if len(route.Methods) > 0 && !contains(route.Methods, req.Method) {
		return false
	}

	// Host check: if the route specifies hosts the request Host must match.
	if len(route.Hosts) > 0 && !contains(route.Hosts, req.Host) {
		return false
	}

	// Path check.
	return matchPath(route.Path, req.URL.Path)
}

// matchPath compares a route path pattern against the request path.
//   - "/health"            matches exactly "/health"
//   - "/api/v1/users/*"    matches "/api/v1/users", "/api/v1/users/123", etc.
//   - "/*"                 matches every path
func matchPath(pattern, path string) bool {
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		// Match the prefix exactly or as a parent directory.
		if prefix == "" {
			return true // "/*" is catch-all
		}
		return strings.HasPrefix(path, prefix)
	}
	return pattern == path
}

// contains reports whether item is present in items.
func contains(items []string, item string) bool {
	for _, i := range items {
		if i == item {
			return true
		}
	}
	return false
}
