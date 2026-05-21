// Package balancer provides load-balancing strategies for distributing
// requests across upstream endpoints.
package balancer

import (
	"net/url"
)

// Balancer selects an upstream endpoint from a list of candidates.
// Implementations must be safe for concurrent use by multiple goroutines.
type Balancer interface {
	// Next returns one endpoint from the list. It must never be called
	// with an empty or nil slice.
	Next(endpoints []*url.URL) *url.URL
}
