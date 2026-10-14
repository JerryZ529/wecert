package webhook

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// BindingRefresher is the console's Refresh action: re-ask the cloud whether the certificates that
// are waiting for their first bind have been bound, and record the answer.
//
// Implemented by the reconciler, which forwards to the ACME manager. Optional in the same way
// BindingReader is -- a deployment that cannot enumerate cloud bindings does not implement it, and
// the route is then not mounted at all.
type BindingRefresher interface {
	RefreshPendingBindings(ctx context.Context, force bool) (checked int, confirmed []string, err error)
}

// bindingRefreshBudget bounds the work one page-triggered refresh may do.
//
// The console aborts the refresh request after 40 seconds (webconsole/console.html), while a
// binding enumeration for a certificate that is bound nowhere polls until its own ~30-second
// budget: the SSL task answers an empty result, which means "not finished" rather than "nothing
// bound". The budget has to cover a FRESH enumeration (IsCache=0) -- the whole point of the click
// is to see a binding made seconds ago, and the cached answer is the one that misses it -- so it
// runs just under the browser's own timeout. Whatever did not finish is left to the daemon's
// ten-minute timer, which runs the same code, and the response says so.
const bindingRefreshBudget = 35 * time.Second

// handleRefreshBindings re-derives the "is this certificate bound yet?" answer for every pending
// certificate, and reports what changed.
//
// POST only, behind the same read-or-admin auth as the inventory it feeds. It reads the cloud and
// records what it observed (the deployment flag); it can neither bind nor unbind anything. The
// binding itself stays an action in the Tencent Cloud console, which is why this page cannot do it
// for the operator.
func (s *Server) handleRefreshBindings(br BindingRefresher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), bindingRefreshBudget)
		defer cancel()

		checked, confirmed, err := br.RefreshPendingBindings(ctx, true)
		if confirmed == nil {
			confirmed = []string{}
		}

		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, map[string]any{
				"checked":    checked,
				"confirmed":  confirmed,
				"incomplete": false,
			})
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			// Out of time, not out of luck: everything that finished is recorded, and the rest is
			// the daemon timer's job. Reporting "incomplete" is what stops the shorter confirmed
			// list from being read as "these are all the certificates that are bound".
			writeJSON(w, http.StatusOK, map[string]any{
				"checked":    checked,
				"confirmed":  confirmed,
				"incomplete": true,
			})
		default:
			s.log.Warn("binding refresh could not run", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "the binding refresh could not run: " + err.Error(),
			})
		}
	}
}
