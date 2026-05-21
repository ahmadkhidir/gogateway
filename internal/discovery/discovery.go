// Package discovery provides service registry interfaces and implementations
// for resolving upstream endpoints by route or service name.
package discovery

import (
	"fmt"
	"net/url"

	"github.com/ahmadkhidir/gogateway/internal/config"
)

// Discoverer resolves a route or service identifier to a list of upstream
// endpoint URLs.
type Discoverer interface {
	// GetEndpoints returns the upstream URLs for the given route/service ID.
	// Returns an error if the route is unknown.
	GetEndpoints(routeID string) ([]*url.URL, error)
}

// StaticRegistry is an in-memory Discoverer built from the config route
// table. It resolves route IDs to their static upstream URLs.
type StaticRegistry struct {
	entries map[string][]*url.URL
}

// NewStaticRegistry builds a StaticRegistry by parsing the Upstream URLs
// from each route. It returns an error if any upstream URL is malformed.
func NewStaticRegistry(routes []config.Route) (*StaticRegistry, error) {
	entries := make(map[string][]*url.URL)
	for i := range routes {
		route := &routes[i]
		var eps []*url.URL
		for _, u := range route.Upstreams {
			parsed, err := url.Parse(u.URL)
			if err != nil {
				return nil, fmt.Errorf("parse upstream URL %q for route %q: %w", u.URL, route.ID, err)
			}
			eps = append(eps, parsed)
		}
		entries[route.ID] = eps
	}
	return &StaticRegistry{entries: entries}, nil
}

// GetEndpoints returns the upstream URLs for the given route ID.
func (s *StaticRegistry) GetEndpoints(routeID string) ([]*url.URL, error) {
	eps, ok := s.entries[routeID]
	if !ok {
		return nil, fmt.Errorf("no endpoints found for route %q", routeID)
	}
	return eps, nil
}
