package webhook

// PUT /admin/certificates/{name} is the console's Renew action when the config
// file is the desired state (static / observe). The guarded surface has to keep
// the failure modes apart -- unwired (404), unreadable body (400) -- and the
// decoded body has to reach the operation intact: a decode error silently
// turned into an empty domain list would clear the declaration.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAdminUpdateCertificateDispatches(t *testing.T) {
	t.Parallel()
	var (
		gotName string
		gotBody UpdateCertificateRequest
	)
	calls := 0
	s := adminServer(t, AdminOps{UpdateCertificate: func(_ context.Context, name string, body UpdateCertificateRequest) (any, error) {
		calls++
		gotName, gotBody = name, body
		return map[string]any{"ok": true, "domains": body.Domains}, nil
	}})
	auth := map[string]string{"Authorization": "Bearer " + adminTok}

	w := do(t, s, http.MethodPut, "/admin/certificates/demo",
		`{"domains":["a.example.com","*.a.example.com"]}`, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if calls != 1 || gotName != "demo" {
		t.Fatalf("calls=%d name=%q, want exactly one call for demo", calls, gotName)
	}
	if len(gotBody.Domains) != 2 || gotBody.Domains[0] != "a.example.com" || gotBody.Domains[1] != "*.a.example.com" {
		t.Errorf("domains = %v, want the decoded list in order", gotBody.Domains)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %s: %v", w.Body.String(), err)
	}
	if body["ok"] != true {
		t.Errorf("body %s must report the operation succeeded", w.Body.String())
	}
}

// A malformed body is a 400 and the operation must not run: a decode error
// silently read as "no domains" would wipe the declaration.
func TestAdminUpdateCertificateRejectsBadBody(t *testing.T) {
	t.Parallel()
	calls := 0
	s := adminServer(t, AdminOps{UpdateCertificate: func(context.Context, string, UpdateCertificateRequest) (any, error) {
		calls++
		return nil, nil
	}})
	auth := map[string]string{"Authorization": "Bearer " + adminTok}

	w := do(t, s, http.MethodPut, "/admin/certificates/demo", `{"domains":`, auth)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT = %d, want 400", w.Code)
	}
	if calls != 0 {
		t.Errorf("the operation ran %d times for an unreadable body", calls)
	}
}

// An unwired operation is a 404, and the name/action split must still route
// .../bind away from the bare name so a bind can never be read as a renew.
func TestAdminUpdateCertificateUnwiredAndActionSplit(t *testing.T) {
	t.Parallel()
	s := adminServer(t, AdminOps{})
	auth := map[string]string{"Authorization": "Bearer " + adminTok}

	w := do(t, s, http.MethodPut, "/admin/certificates/demo", `{"domains":["a.example.com"]}`, auth)
	if w.Code != http.StatusNotFound {
		t.Fatalf("PUT = %d, want 404 (body %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not wired") {
		t.Errorf("body %s must name the unwired operation", w.Body.String())
	}

	// The same PUT against the bind action stays a method error: actions are
	// their own routes, and a renew must not be reachable through one.
	w = do(t, s, http.MethodPut, "/admin/certificates/demo/bind", `{"domains":["a.example.com"]}`, auth)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT .../bind = %d, want 405", w.Code)
	}
}
