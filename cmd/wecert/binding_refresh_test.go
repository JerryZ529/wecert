package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type countingRefresher struct {
	mu    sync.Mutex
	calls int
	force bool
}

func (c *countingRefresher) RefreshPendingBindings(_ context.Context, force bool) (int, []string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.force = force
	return 1, nil, nil
}

func (c *countingRefresher) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingRefresher) forced() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.force
}

// The timer is what keeps the binding answer fresh between passes, and stopping it has to return
// promptly instead of waiting out an interval.
func TestBindingRefreshTimerRefreshesAndStops(t *testing.T) {
	refresher := &countingRefresher{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	stop := startBindingRefresh(context.Background(), 5*time.Millisecond, refresher, log)

	deadline := time.Now().Add(2 * time.Second)
	for refresher.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := refresher.count(); got < 2 {
		t.Fatalf("the timer ran the refresh %d times, want at least 2", got)
	}

	stop()

	after := refresher.count()
	time.Sleep(30 * time.Millisecond)
	if got := refresher.count(); got != after {
		t.Errorf("the refresh ran %d more time(s) after stop", got-after)
	}

	// The periodic caller wants the pacing; only the console's click asks for the answer now.
	if refresher.forced() {
		t.Error("the timer must respect the throttle, so it must not force the check")
	}
}
