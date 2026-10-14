package acme

// The pending-binding refresh is the fast lane for "a human just bound it in
// the console". It must force a fresh cloud-side enumeration (IsCache=0):
// the cached answer can be half an hour old and is precisely the one known to
// predate their change. The routine per-pass check keeps the cached lookup.

import (
	"context"
	"testing"
)

// freshDeployer answers both entry points so a test can see which one was used.
type freshDeployer struct {
	fakeDeployer
	freshCalls  int
	freshResult int
}

func (f *freshDeployer) BindingsFresh(_ context.Context, _ string) (int, bool, error) {
	f.freshCalls++
	return f.freshResult, true, nil
}

func TestRefreshPendingBindingsForcesAFreshScan(t *testing.T) {
	t.Parallel()
	dep := &freshDeployer{freshResult: 2}
	m, store, _ := newConfirmHarness(t, dep, nil)
	_ = store

	checked, confirmed, err := m.RefreshPendingBindings(context.Background(), false)
	if err != nil {
		t.Fatalf("RefreshPendingBindings: %v", err)
	}
	if dep.freshCalls != 1 || dep.calls != 0 {
		t.Errorf("fresh=%d cached=%d, want the fresh enumeration only", dep.freshCalls, dep.calls)
	}
	if checked != 1 || len(confirmed) != 1 || confirmed[0] != "confirm-test" {
		t.Fatalf("checked=%d confirmed=%v, want the certificate confirmed", checked, confirmed)
	}
	st, err := m.store.GetCert("confirm-test")
	if err != nil {
		t.Fatal(err)
	}
	if !st.DeployConfirmed {
		t.Error("the fresh answer said 2 bound resources, so DeployConfirmed must be set")
	}
}

// A deployer that cannot force a fresh scan still answers from its cache:
// older or non-Tencent deployments must keep working.
func TestRefreshPendingBindingsFallsBackWithoutTheFreshCapability(t *testing.T) {
	t.Parallel()
	dep := &fakeDeployer{bindings: 1}
	m, _, _ := newConfirmHarness(t, dep, nil)

	checked, confirmed, err := m.RefreshPendingBindings(context.Background(), false)
	if err != nil {
		t.Fatalf("RefreshPendingBindings: %v", err)
	}
	if dep.calls != 1 {
		t.Errorf("cached Bindings calls = %d, want 1", dep.calls)
	}
	if checked != 1 || len(confirmed) != 1 || confirmed[0] != "confirm-test" {
		t.Fatalf("checked=%d confirmed=%v, want the certificate confirmed", checked, confirmed)
	}
}

// The routine per-pass confirmation keeps the cached lookup: forcing a full
// enumeration on every pass is the API storm the cache exists to prevent.
func TestReconcileConfirmationKeepsTheCachedLookup(t *testing.T) {
	t.Parallel()
	dep := &freshDeployer{freshResult: 1}
	m, _, cert := newConfirmHarness(t, dep, nil)

	if err := m.Reconcile(context.Background(), cert); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if dep.freshCalls != 0 || dep.calls != 1 {
		t.Errorf("fresh=%d cached=%d, want the routine pass to use the cached lookuper only", dep.freshCalls, dep.calls)
	}
}
