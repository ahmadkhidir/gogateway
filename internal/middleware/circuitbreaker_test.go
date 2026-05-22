package middleware

import (
	"testing"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/config"
)

func testBreakerCfg() config.CircuitBrkCfg {
	return config.CircuitBrkCfg{
		Enabled:   true,
		Threshold: 3,
		Timeout:   50 * time.Millisecond,
	}
}

func TestCircuitBreaker_InitialState_Closed(t *testing.T) {
	cb := NewCircuitBreaker(testBreakerCfg())
	state, _, _, _ := cb.StateSnapshot()
	if state != StateClosed {
		t.Errorf("expected initial state Closed, got %v", state)
	}
}

func TestCircuitBreaker_AllowWhenClosed(t *testing.T) {
	cb := NewCircuitBreaker(testBreakerCfg())
	for i := 0; i < 10; i++ {
		if !cb.Allow() {
			t.Errorf("iteration %d: expected Allow()=true when Closed", i+1)
		}
	}
}

func TestCircuitBreaker_TripsAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(testBreakerCfg())

	// Record failures up to threshold.
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	state, count, _, _ := cb.StateSnapshot()
	if state != StateOpen {
		t.Errorf("expected StateOpen after threshold failures, got %v", state)
	}
	if count != 3 {
		t.Errorf("expected failure count 3, got %d", count)
	}

	// Should reject requests while open.
	if cb.Allow() {
		t.Error("expected Allow()=false when circuit is Open")
	}
}

func TestCircuitBreaker_RejectsBelowThreshold(t *testing.T) {
	cb := NewCircuitBreaker(testBreakerCfg())

	// 2 failures, threshold is 3 → should stay closed.
	cb.RecordFailure()
	cb.RecordFailure()

	if !cb.Allow() {
		t.Error("expected Allow()=true when below threshold")
	}
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(config.CircuitBrkCfg{
		Enabled:   true,
		Threshold: 2,
		Timeout:   30 * time.Millisecond,
	})

	// Trip the breaker.
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.Allow() {
		t.Error("expected Allow()=false immediately after tripping")
	}

	// Wait for timeout.
	time.Sleep(40 * time.Millisecond)

	// Should transition to HalfOpen and allow a probe.
	if !cb.Allow() {
		t.Error("expected Allow()=true after timeout (HalfOpen)")
	}

	state, _, _, _ := cb.StateSnapshot()
	if state != StateHalfOpen {
		t.Errorf("expected StateHalfOpen after timeout, got %v", state)
	}
}

func TestCircuitBreaker_ProbeSuccess_Closes(t *testing.T) {
	cb := NewCircuitBreaker(config.CircuitBrkCfg{
		Enabled:   true,
		Threshold: 2,
		Timeout:   30 * time.Millisecond,
	})

	// Trip.
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for timeout → HalfOpen.
	time.Sleep(40 * time.Millisecond)
	cb.Allow() // triggers transition to HalfOpen

	// Probe succeeds.
	cb.RecordSuccess()

	state, count, _, _ := cb.StateSnapshot()
	if state != StateClosed {
		t.Errorf("expected StateClosed after successful probe, got %v", state)
	}
	if count != 0 {
		t.Errorf("expected failure count 0 after close, got %d", count)
	}

	// Normal operation resumes.
	if !cb.Allow() {
		t.Error("expected Allow()=true after circuit resets to Closed")
	}
}

func TestCircuitBreaker_ProbeFailure_Reopens(t *testing.T) {
	cb := NewCircuitBreaker(config.CircuitBrkCfg{
		Enabled:   true,
		Threshold: 2,
		Timeout:   30 * time.Millisecond,
	})

	// Trip.
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for timeout → HalfOpen.
	time.Sleep(40 * time.Millisecond)
	cb.Allow() // triggers transition to HalfOpen

	// Probe fails.
	cb.RecordFailure()

	state, _, _, _ := cb.StateSnapshot()
	if state != StateOpen {
		t.Errorf("expected StateOpen after failed probe, got %v", state)
	}
}

func TestCircuitBreaker_SuccessResetsCount(t *testing.T) {
	cb := NewCircuitBreaker(testBreakerCfg())

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // resets count to 0

	state, count, _, _ := cb.StateSnapshot()
	if state != StateClosed {
		t.Errorf("expected StateClosed after success, got %v", state)
	}
	if count != 0 {
		t.Errorf("expected failure count 0 after success, got %d", count)
	}
}

func TestCircuitBreaker_FailureThenSuccessDoesNotTrip(t *testing.T) {
	cb := NewCircuitBreaker(testBreakerCfg())

	// Pattern: fail, fail, success, fail — should not trip (success resets).
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess()
	cb.RecordFailure()

	if !cb.Allow() {
		t.Error("expected Allow()=true — success reset the counter before trip")
	}
}

func TestCircuitBreaker_ConcurrentSafety(t *testing.T) {
	cb := NewCircuitBreaker(testBreakerCfg())

	done := make(chan bool)

	// Concurrent failures.
	go func() {
		for i := 0; i < 50; i++ {
			cb.RecordFailure()
		}
		done <- true
	}()

	// Concurrent successes.
	go func() {
		for i := 0; i < 50; i++ {
			cb.RecordSuccess()
		}
		done <- true
	}()

	// Concurrent Allow calls.
	go func() {
		for i := 0; i < 50; i++ {
			cb.Allow()
		}
		done <- true
	}()

	<-done
	<-done
	<-done
	// If we get here without data races, the test passes.
}

func TestBreakerStore_GetOrCreate(t *testing.T) {
	bs := NewBreakerStore()
	cfg := testBreakerCfg()

	cb1 := bs.GetOrCreate("route-a", "http://upstream:1", cfg)
	if cb1 == nil {
		t.Fatal("expected non-nil breaker")
	}

	// Same key returns the same breaker.
	cb2 := bs.GetOrCreate("route-a", "http://upstream:1", cfg)
	if cb1 != cb2 {
		t.Error("expected same breaker instance for same key")
	}

	// Different key returns a different breaker.
	cb3 := bs.GetOrCreate("route-b", "http://other:1", cfg)
	if cb1 == cb3 {
		t.Error("expected different breaker for different key")
	}
}

func TestBreakerStore_Snapshot(t *testing.T) {
	bs := NewBreakerStore()
	cfg := testBreakerCfg()

	bs.GetOrCreate("r1", "http://a:1", cfg)
	bs.GetOrCreate("r2", "http://b:1", cfg)

	snap := bs.Snapshot()
	if len(snap) != 2 {
		t.Errorf("expected 2 entries in snapshot, got %d", len(snap))
	}
	for _, state := range snap {
		if state != StateClosed {
			t.Errorf("expected all breakers Closed initially, got %v", state)
		}
	}
}

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		state CircuitState
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{CircuitState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("CircuitState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
