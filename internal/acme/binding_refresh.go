package acme

import "context"

// RefreshPendingBindings re-asks the cloud whether every uploaded-but-unconfirmed certificate is
// bound yet, and records what it observed in the state store.
//
// Why this exists next to confirmBindingIfDue, which the pass already calls: the pass runs on the
// reconciliation interval (an hour by default), while the event that changes this answer is a
// person binding the certificate in the Tencent Cloud console minutes after it was uploaded. Until
// the answer lands, the inventory page says "bind needed" for a certificate that is serving
// traffic, and the metrics report the deployment as unconfirmed. So the daemon calls this on its
// own ten-minute timer, and the console's Refresh button calls it with force -- an operator who
// has just done something in the cloud should not be told to wait for the next tick.
//
// Read-only against the cloud: it enumerates bindings and writes what it found to the state store.
// It never binds, rebinds or deletes anything, and a certificate whose enumeration says "nothing
// bound" simply stays unconfirmed.
//
// force skips the throttle window only. Both callers record the attempt either way, so a forced
// check pushes the next automatic one out by the window instead of doubling it.
//
// One certificate's failure is logged and does not stop the others: the caller is a background
// timer or a page load, and one unreachable region must not silence the rest of the account.
func (m *Manager) RefreshPendingBindings(ctx context.Context, force bool) (checked int, confirmed []string, err error) {
	names, err := m.store.ListCertNames()
	if err != nil {
		return 0, nil, err
	}

	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return checked, confirmed, err
		}
		st, gerr := m.store.GetCert(name)
		if gerr != nil || st == nil {
			continue
		}
		// The same gate confirmBindingIfDue applies: only a certificate that was uploaded through
		// wecert and not yet confirmed as bound is worth asking about. DeployedCertID is set by the
		// upload, which only happens when the certificate's deploy is enabled, so the running
		// configuration is not needed here to tell "waiting for its first bind" from "nothing to
		// confirm".
		if st.DeployedCertID == "" || st.DeployConfirmed {
			continue
		}
		if !force && !m.bindingCheckReady(name) {
			continue
		}
		m.bindingNoteAttempt(name)
		checked++

		if cerr := m.confirmBinding(ctx, name, st); cerr != nil {
			if err := ctx.Err(); err != nil {
				return checked, confirmed, err
			}
			m.log.Warn("could not confirm the certificate binding", "cert", name, "certId", st.DeployedCertID, "err", cerr)
			continue
		}
		if st.DeployConfirmed {
			confirmed = append(confirmed, name)
		}
	}
	return checked, confirmed, nil
}
