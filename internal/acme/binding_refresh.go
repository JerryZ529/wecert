package acme

import (
	"context"
	"sort"
	"sync"

	"github.com/susunola/wecert/internal/deploy"
)

// RefreshPendingBindings re-asks the cloud about the certificates whose binding answer this process
// does not currently have, and records what it observed.
//
// Two kinds of candidate, and the difference is what force is for:
//
//   - a certificate waiting for its first bind: until the binding is noticed, the inventory says
//     "bind needed" for a certificate that is serving traffic, and the deployed metric reports
//     false. The daemon's ten-minute timer asks about these, and the throttle paces it.
//   - a certificate that is confirmed but whose binding rows are no longer cached: the page can then
//     show no load balancer, no name and no region, and "details not reported" is all an operator
//     sees. Only an explicit refresh asks about these -- the answer changes when someone edits the
//     load balancer in the Tencent Cloud console, so re-enumerating rows that are already displayed
//     would spend a cloud call per click for no visible change.
//
// Why this exists next to confirmBindingIfDue, which the pass already calls: the pass runs on the
// reconciliation interval (an hour by default), while the event that changes these answers is a
// person working in the Tencent Cloud console. A page re-read seconds later must not report the
// state from before their change.
//
// The certificate WAITING FOR ITS FIRST BIND is looked up with a forced fresh cloud-side
// enumeration (IsCache=0): the cached answer is the one known to predate the operator's change,
// and waiting for the periodic account-wide patrol to refresh it takes hours. The confirmed rows
// (the ones that only need their binding details re-rendered) keep the cached lookup -- a fresh
// scan costs seconds per certificate, and the refresh budget belongs to the certificate a human
// is actually waiting on.
//
// Read-only against the cloud: it enumerates bindings and writes what it found to the state store.
// It never binds, rebinds or deletes anything, and a certificate whose enumeration says "nothing
// bound" simply stays unconfirmed.
//
// One certificate's failure is logged and does not stop the others: the caller is a background
// timer or a page load, and one unreachable region must not silence the rest of the account.
//
// The lookups run concurrently, under a small bound. Each one is an independent cloud round trip
// that polls until its enumeration finishes -- seconds apiece -- so a serial walk makes the whole
// request as slow as the sum. With a handful of certificates that exceeds the page's budget and
// the operator is told "still running" even though every individual answer had arrived; the bound
// keeps a large fleet from turning one click into a fan-out.
func (m *Manager) RefreshPendingBindings(ctx context.Context, force bool) (checked int, confirmed []string, err error) {
	names, err := m.store.ListCertNames()
	if err != nil {
		return 0, nil, err
	}

	const maxParallel = 4
	sem := make(chan struct{}, maxParallel)
	var (
		mu                sync.Mutex
		wg                sync.WaitGroup
		checkedN          int
		ok                []string
		rowRefreshStarted bool
	)
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if ctx.Err() != nil {
				return
			}

			st, gerr := m.store.GetCert(name)
			if gerr != nil || st == nil || st.DeployedCertID == "" {
				return
			}

			if st.DeployConfirmed {
				// Refresh the rows the page renders, not the deployment flag: this certificate
				// is already confirmed, so there is nothing to confirm and no state to write.
				if !force {
					return
				}
				if _, _, cached := deploy.LookupCachedBindings(st.DeployedCertID); cached {
					return
				}
				// At most one row-refresh per request. An uncached enumeration can take
				// ~30 seconds on the cloud side, and the refresh budget belongs to the
				// certificate a human is actually waiting on; the remaining rows are
				// picked up by the next click (or by the periodic patrol).
				mu.Lock()
				if rowRefreshStarted {
					mu.Unlock()
					return
				}
				rowRefreshStarted = true
				checkedN++
				mu.Unlock()
				// Cached on purpose: this refreshes rows the page renders, and a fresh
				// enumeration here would spend the budget on certificates whose binding is
				// already confirmed -- leaving none for the one actually waiting.
				if _, _, berr := m.deployer.Bindings(ctx, st.DeployedCertID); berr != nil {
					if ctx.Err() == nil {
						m.log.Warn("could not enumerate the bindings shown for this certificate",
							"cert", name, "certId", st.DeployedCertID, "err", berr)
					}
				}
				return
			}

			// Waiting for the first bind. DeployedCertID is set by the upload, which only
			// happens when the certificate's deploy is enabled, so the running configuration
			// is not needed here to tell "waiting for its first bind" from "nothing to
			// confirm".
			if !force && !m.bindingCheckReady(name) {
				return
			}
			m.bindingNoteAttempt(name)
			mu.Lock()
			checkedN++
			mu.Unlock()

			// fresh=true: this is the fast lane for "someone just bound it". The cached
			// cloud answer is the one known to predate their change, so it is the wrong
			// answer to show the operator who just clicked Refresh.
			if cerr := m.confirmBinding(ctx, name, st, true); cerr != nil {
				if ctx.Err() == nil {
					m.log.Warn("could not confirm the certificate binding",
						"cert", name, "certId", st.DeployedCertID, "err", cerr)
				}
				return
			}
			if st.DeployConfirmed {
				mu.Lock()
				ok = append(ok, name)
				mu.Unlock()
			}
		}(name)
	}
	wg.Wait()

	checked = checkedN
	if len(ok) > 0 {
		sort.Strings(ok)
		confirmed = ok
	}
	// A cancelled or exhausted request reports what finished plus the reason, so the caller can
	// answer "incomplete" instead of pretending the list is whole.
	if cerr := ctx.Err(); cerr != nil {
		return checked, confirmed, cerr
	}
	return checked, confirmed, nil
}
