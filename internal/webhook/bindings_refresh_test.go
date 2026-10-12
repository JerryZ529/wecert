package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// fakeBindingRefresher is the optional capability, embedded next to the mandatory fake reconciler
// so the route mounts (see the same trick in txt_reclaims_test.go).
type fakeBindingRefresher struct {
	checked   int
	confirmed []string
	err       error
	calls     int
	force     bool
}

func (f *fakeBindingRefresher) RefreshPendingBindings(_ context.Context, force bool) (int, []string, error) {
	f.calls++
	f.force = force
	return f.checked, f.confirmed, f.err
}

func newRefreshServer(t *testing.T, fake *fakeBindingRefresher) *Server {
	t.Helper()
	s, _ := newTestServer(t, &struct {
		*fakeReconciler
		*fakeBindingRefresher
	}{&fakeReconciler{names: []string{"jerryzhou-live"}}, fake})
	return s
}

func TestBindingRefreshIsPostOnlyAndReportsWhatChanged(t *testing.T) {
	fake := &fakeBindingRefresher{checked: 2, confirmed: []string{"jerryzhou-live"}}
	s := newRefreshServer(t, fake)

	w := do(t, s, http.MethodPost, "/api/bindings/refresh", "{}", bearer())
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Checked    int      `json:"checked"`
		Confirmed  []string `json:"confirmed"`
		Incomplete bool     `json:"incomplete"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Checked != 2 || len(body.Confirmed) != 1 || body.Confirmed[0] != "jerryzhou-live" || body.Incomplete {
		t.Fatalf("unexpected body %+v", body)
	}
	// The console's click is the forced path: it must not sit behind the timer's throttle.
	if !fake.force {
		t.Error("a page-triggered refresh must ask for the answer now, not when the throttle allows")
	}

	if w := do(t, s, http.MethodGet, "/api/bindings/refresh", "", bearer()); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", w.Code)
	}
	if w := do(t, s, http.MethodPost, "/api/bindings/refresh", "{}", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no auth = %d, want 401", w.Code)
	}
}

// Running out of the request budget is not an error to report: everything that finished is
// recorded, the daemon timer finishes the rest, and the page still has to be told the list is
// partial -- otherwise a shorter confirmed list reads as the whole set.
func TestBindingRefreshReportsAnIncompleteAnswer(t *testing.T) {
	fake := &fakeBindingRefresher{checked: 1, err: context.DeadlineExceeded}
	s := newRefreshServer(t, fake)

	w := do(t, s, http.MethodPost, "/api/bindings/refresh", "{}", bearer())
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Incomplete bool `json:"incomplete"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Incomplete {
		t.Error("a budget that ran out must be reported as incomplete")
	}
}

// A failure that is not the budget is still a failure, and it must not be dressed up as an answer.
func TestBindingRefreshReportsAFailure(t *testing.T) {
	fake := &fakeBindingRefresher{err: errors.New("the cloud account is unreachable")}
	s := newRefreshServer(t, fake)

	if w := do(t, s, http.MethodPost, "/api/bindings/refresh", "{}", bearer()); w.Code != http.StatusServiceUnavailable {
		t.Errorf("POST = %d, want 503", w.Code)
	}
}

// Without the capability the route is not mounted at all, rather than answering with an empty set
// of confirmations.
func TestBindingRefreshWithoutTheCapabilityIsNotMounted(t *testing.T) {
	s, _ := newTestServer(t, &fakeReconciler{names: []string{"a"}})
	if w := do(t, s, http.MethodPost, "/api/bindings/refresh", "{}", bearer()); w.Code == http.StatusOK {
		t.Error("the route must not answer when the reconciler cannot refresh bindings")
	}
}
