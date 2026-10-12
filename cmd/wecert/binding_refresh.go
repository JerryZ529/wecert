package main

import (
	"context"
	"log/slog"
	"time"
)

// bindingRefreshEvery is how often the daemon re-asks whether a certificate waiting for its first
// bind has been bound in the Tencent Cloud console.
//
// Ten minutes, because this is the one piece of state whose answer changes by a human action the
// same human then expects to see the result of: they bind the certificate in the console and open
// the inventory page. Until the answer is recorded the page says "bind needed" for a certificate
// that is already serving, and the deployed metric reports false. The pass runs on the reconcile
// interval -- an hour by default, and the reason no separate timer existed -- so "wait for the next
// pass" was the standing advice for a change made two minutes ago.
//
// The cost stays bounded by the manager's throttle: the timer and the pass share it, so the account
// still sees one enumeration per pending certificate per window, and only certificates waiting for
// their first bind are ever asked about.
const bindingRefreshEvery = 10 * time.Minute

// bindingRefresher is the part of the runtime this timer needs. An interface rather than
// *runtimeController so the loop is testable with a fake, the same way the pass is injected into
// runDaemon.
type bindingRefresher interface {
	RefreshPendingBindings(ctx context.Context, force bool) (checked int, confirmed []string, err error)
}

// startBindingRefresh runs the binding refresh between passes and returns a stop function, in the
// shape startStateBackups established.
//
// Its own context, so shutdown does not depend on the process context having been cancelled:
// run() returns normally from -once and from a daemon stop, and signal.NotifyContext's stop() does
// not cancel. Waiting on ctx.Done() there would have hung forever.
func startBindingRefresh(ctx context.Context, every time.Duration, r bindingRefresher, log *slog.Logger) func() {
	refreshCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go bindingRefreshLoop(refreshCtx, every, r, log, done)
	log.Info("binding refresh timer is on", "every", every)
	return func() { stopBindingRefresh(cancel, done, log) }
}

// bindingRefreshLoop re-asks once per interval until ctx is done, closing done as it returns.
//
// No immediate run: the pass the daemon starts with covers the moment the process comes up, and
// firing here as well would spend the throttle window twice for one answer.
func bindingRefreshLoop(ctx context.Context, every time.Duration, r bindingRefresher, log *slog.Logger, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checked, confirmed, err := r.RefreshPendingBindings(ctx, false)
			if err != nil {
				log.Warn("binding refresh failed", "err", err)
				continue
			}
			if len(confirmed) > 0 {
				log.Info("binding refresh confirmed certificates as bound", "certs", confirmed, "checked", checked)
			}
		}
	}
}

// stopBindingRefresh cancels the loop and waits for it, bounded the way stopSnapshotLoop is: a
// stop signal must never hang shutdown, and what it waits on here is a read-only enumeration.
func stopBindingRefresh(cancel context.CancelFunc, done <-chan struct{}, log *slog.Logger) {
	cancel()
	select {
	case <-done:
	case <-time.After(bindingRefreshDrainTimeout):
		log.Warn("a binding refresh was still running at shutdown; the state store is about to be "+
			"closed under it", "waited", bindingRefreshDrainTimeout)
	}
}

// bindingRefreshDrainTimeout bounds how long shutdown waits for an in-flight binding refresh.
//
// Shorter than the enumeration's own budget on purpose: the enumeration takes its time on a
// certificate that is bound nowhere (the SSL task keeps answering "not finished"), and that answer
// is not worth holding up a restart for.
const bindingRefreshDrainTimeout = 5 * time.Second
