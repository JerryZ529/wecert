package reconcile

import (
	"context"
	"errors"
)

// ErrBindingRefreshUnsupported means the configured manager cannot enumerate cloud bindings, so
// there is nothing to refresh. It is an error rather than an empty answer on purpose: "we could
// not ask" and "the answer is no bindings" are different claims, and only the second one may be
// shown as a count.
var ErrBindingRefreshUnsupported = errors.New("this deployment cannot enumerate cloud bindings")

// BindingRefresher is the optional capability a manager has when it can re-ask the cloud whether
// an uploaded-but-unconfirmed certificate is bound yet, without running a whole pass.
//
// Optional in the same way BindingPatroller is, and for the same reason: a manager with no cloud
// bindings to look up simply does not implement it, and the routes and timers that would call it
// are then not wired up.
//
// force skips the manager's throttle window -- an operator who clicked Refresh wants the answer
// now, while the periodic caller wants the pacing.
type BindingRefresher interface {
	RefreshPendingBindings(ctx context.Context, force bool) (checked int, confirmed []string, err error)
}

// RefreshPendingBindings forwards to the manager, and reports that the capability is missing
// rather than pretending nothing needed refreshing.
//
// The distinction matters to the caller: an empty answer from a manager that cannot enumerate
// bindings looks exactly like "nothing is bound", which is the one claim this project refuses to
// make without evidence.
func (r *Reconciler) RefreshPendingBindings(ctx context.Context, force bool) (int, []string, error) {
	p, ok := r.manager.(BindingRefresher)
	if !ok {
		return 0, nil, ErrBindingRefreshUnsupported
	}
	return p.RefreshPendingBindings(ctx, force)
}
