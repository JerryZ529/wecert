package reconcile

import (
	"context"
	"errors"
	"testing"
)

// fakeBindingRefresher adds the optional capability to the shared fake manager, so the forwarding
// can be observed without a cloud API.
type fakeBindingRefresher struct {
	*fakeManager
	checked   int
	confirmed []string
	calls     int
	force     bool
}

func (f *fakeBindingRefresher) RefreshPendingBindings(_ context.Context, force bool) (int, []string, error) {
	f.calls++
	f.force = force
	return f.checked, f.confirmed, nil
}

// The reconciler is the webhook's and the daemon's handle on the manager, so the capability has to
// be reachable through it -- and the force flag has to survive the hop: a click means "now".
func TestRefreshPendingBindingsForwardsToTheManager(t *testing.T) {
	mgr := &fakeBindingRefresher{
		fakeManager: &fakeManager{},
		checked:     2,
		confirmed:   []string{"a"},
	}
	r, _ := newTestReconciler(t, []string{"a"}, mgr)

	checked, confirmed, err := r.RefreshPendingBindings(context.Background(), true)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if mgr.calls != 1 || !mgr.force {
		t.Errorf("calls=%d force=%v, want exactly one forced call", mgr.calls, mgr.force)
	}
	if checked != 2 || len(confirmed) != 1 || confirmed[0] != "a" {
		t.Errorf("checked=%d confirmed=%v, want the manager's own answer", checked, confirmed)
	}
}

// A manager that cannot enumerate bindings has to say so: an empty answer reads as "bound nowhere",
// which is the one claim this project refuses to make without evidence.
func TestRefreshPendingBindingsWithoutTheCapabilityIsAnError(t *testing.T) {
	r, _ := newTestReconciler(t, []string{"a"}, &fakeManager{})

	if _, _, err := r.RefreshPendingBindings(context.Background(), false); !errors.Is(err, ErrBindingRefreshUnsupported) {
		t.Fatalf("err = %v, want ErrBindingRefreshUnsupported", err)
	}
}
