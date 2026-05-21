package balancer

import (
	"net/url"
	"sync"
	"testing"
)

func parseURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}

func TestRoundRobin_Distribution(t *testing.T) {
	eps := []*url.URL{
		parseURL("http://a:1"),
		parseURL("http://b:1"),
		parseURL("http://c:1"),
	}

	rr := NewRoundRobin()
	counts := make(map[string]int)

	const n = 300
	for i := 0; i < n; i++ {
		ep := rr.Next(eps)
		counts[ep.String()]++
	}

	// Each endpoint should get exactly n/3 hits.
	for _, ep := range eps {
		if counts[ep.String()] != 100 {
			t.Errorf("expected 100 hits for %s, got %d", ep.String(), counts[ep.String()])
		}
	}
}

func TestRoundRobin_SingleEndpoint(t *testing.T) {
	ep := parseURL("http://single:1")
	rr := NewRoundRobin()

	for i := 0; i < 10; i++ {
		got := rr.Next([]*url.URL{ep})
		if got.String() != ep.String() {
			t.Errorf("iteration %d: expected %s, got %s", i, ep.String(), got.String())
		}
	}
}

func TestRoundRobin_ConcurrentSafety(t *testing.T) {
	eps := []*url.URL{
		parseURL("http://a:1"),
		parseURL("http://b:1"),
	}

	rr := NewRoundRobin()
	var wg sync.WaitGroup

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				rr.Next(eps)
			}
		}()
	}

	wg.Wait()
	// If we got here without a race, the test passes.
}

func TestLeastConnections_Basic(t *testing.T) {
	eps := []*url.URL{
		parseURL("http://a:1"),
		parseURL("http://b:1"),
	}

	lc := NewLeastConnections()

	// First call should pick 'a' (both at 0, first wins).
	ep1 := lc.Next(eps)
	if ep1.String() != "http://a:1" {
		t.Errorf("expected first pick 'a', got %s", ep1.String())
	}

	// Second call: a has 1 conn, b has 0 — should pick b.
	ep2 := lc.Next(eps)
	if ep2.String() != "http://b:1" {
		t.Errorf("expected second pick 'b', got %s", ep2.String())
	}

	// Mark a as done.
	lc.Done(eps[0])

	// Now: a=0, b=1 — should pick a.
	ep3 := lc.Next(eps)
	if ep3.String() != "http://a:1" {
		t.Errorf("expected third pick 'a', got %s", ep3.String())
	}
}

func TestLeastConnections_DoneUnderflow(t *testing.T) {
	// Calling Done more than Next should not produce negative counts.
	ep := parseURL("http://x:1")
	lc := NewLeastConnections()

	lc.Done(ep)
	lc.Done(ep)

	// Should not panic; count should be 0.
	got := lc.Next([]*url.URL{ep})
	if got.String() != ep.String() {
		t.Errorf("expected %s, got %s", ep.String(), got.String())
	}
}
