package balancer

import (
	"net/url"
	"sync"
)

// LeastConnections tracks active connection counts per endpoint and picks
// the one with the fewest active connections. Callers must invoke Done()
// after the proxied request completes to decrement the count.
type LeastConnections struct {
	mu    sync.Mutex
	conns map[string]int64
}

// NewLeastConnections creates a new LeastConnections balancer.
func NewLeastConnections() *LeastConnections {
	return &LeastConnections{
		conns: make(map[string]int64),
	}
}

// Next selects the endpoint with the fewest active connections and
// increments its count.
func (lc *LeastConnections) Next(endpoints []*url.URL) *url.URL {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	var best *url.URL
	var bestCount int64 = 1<<63 - 1 // MaxInt64

	for _, ep := range endpoints {
		key := ep.String()
		count := lc.conns[key]
		if count < bestCount {
			bestCount = count
			best = ep
		}
	}

	lc.conns[best.String()]++
	return best
}

// Done decrements the active connection count for a previously selected
// endpoint. Call this in a defer after the upstream request completes.
func (lc *LeastConnections) Done(endpoint *url.URL) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	key := endpoint.String()
	lc.conns[key]--
	if lc.conns[key] < 0 {
		lc.conns[key] = 0
	}
}
