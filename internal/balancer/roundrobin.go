package balancer

import (
	"net/url"
	"sync"
)

// RoundRobin distributes requests across endpoints in a cyclic order.
// A single mutex-protected counter ensures fairness across goroutines.
type RoundRobin struct {
	mu      sync.Mutex
	counter uint64
}

// NewRoundRobin creates a new RoundRobin balancer.
func NewRoundRobin() *RoundRobin {
	return &RoundRobin{}
}

// Next returns the next endpoint in round-robin order.
func (rr *RoundRobin) Next(endpoints []*url.URL) *url.URL {
	rr.mu.Lock()
	n := uint64(len(endpoints))
	i := rr.counter % n
	rr.counter++
	rr.mu.Unlock()
	return endpoints[i]
}
