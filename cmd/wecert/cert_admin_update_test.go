package main

// Renew in config-driven mode rewrites one certificate's declared domain set
// inside the daemon configuration. The rewrite must touch exactly that entry:
// dropping a neighbouring certificate or one of its fields is a change nobody
// asked for, and the reload would then reconcile a fleet the operator never
// declared.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const staticUpdateFixture = `statePath: /tmp/wecert-update-test.db
tencent:
  credentialMode: static
  secretId: id
  secretKey: key
  regions: [ap-guangzhou]
certificates:
  - name: demo
    domains: [demo.example.com]
    profile: classic
    keyType: ecdsa-p256
    deploy:
      enabled: true
  - name: other
    domains:
      - other.example.com
    profile: classic
    keyType: ecdsa-p256
    deploy:
      enabled: false
webhook:
  listen: 127.0.0.1:9801
`

func writeStaticUpdateFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(staticUpdateFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readCertificateEntries(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Certificates []map[string]any `yaml:"certificates"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the rewritten config no longer parses: %v", err)
	}
	out := map[string]map[string]any{}
	for _, entry := range doc.Certificates {
		name, _ := entry["name"].(string)
		out[name] = entry
	}
	return out
}

func TestUpdateCertificateDomainsRewritesOnlyTheNamedEntry(t *testing.T) {
	path := writeStaticUpdateFixture(t)
	want := []string{"demo.example.com", "www.demo.example.com"}
	if err := updateCertificateDomainsInConfig(path, "demo", want); err != nil {
		t.Fatalf("updateCertificateDomainsInConfig: %v", err)
	}

	entries := readCertificateEntries(t, path)
	if len(entries) != 2 {
		t.Fatalf("certificates = %d entries, want both kept", len(entries))
	}
	demo := entries["demo"]
	got, _ := demo["domains"].([]any)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("demo domains = %v, want %v", demo["domains"], want)
	}
	if demo["profile"] != "classic" || demo["keyType"] != "ecdsa-p256" {
		t.Errorf("the rewrite dropped fields of the edited entry: %v", demo)
	}
	if deploy, ok := demo["deploy"].(map[string]any); !ok || deploy["enabled"] != true {
		t.Errorf("the rewrite dropped demo's deploy block: %v", demo["deploy"])
	}

	other := entries["other"]
	otherDomains, _ := other["domains"].([]any)
	if len(otherDomains) != 1 || otherDomains[0] != "other.example.com" {
		t.Errorf("the neighbouring entry's domains changed: %v", other["domains"])
	}
	if deploy, ok := other["deploy"].(map[string]any); !ok || deploy["enabled"] != false {
		t.Errorf("the neighbouring entry's deploy block changed: %v", other["deploy"])
	}
}

func TestUpdateCertificateDomainsRefusesAnUndeclaredName(t *testing.T) {
	path := writeStaticUpdateFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = updateCertificateDomainsInConfig(path, "ghost", []string{"ghost.example.com"})
	if err == nil {
		t.Fatal("an undeclared name must be refused: renew edits a declaration, it does not create one")
	}
	if !strings.Contains(err.Error(), "not declared") {
		t.Errorf("error %v must say the name is not declared", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a refused update must leave the file untouched")
	}
}

// A declaration that only carries the name (the deploy/clk defaults shape) has
// no domains list yet; renew has to add one rather than fail on the missing key.
func TestUpdateCertificateDomainsAddsTheFieldWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	fixture := `statePath: /tmp/wecert-update-test.db
certificates:
  - name: bare
    deploy:
      enabled: true
`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := updateCertificateDomainsInConfig(path, "bare", []string{"bare.example.com"}); err != nil {
		t.Fatalf("updateCertificateDomainsInConfig: %v", err)
	}
	entries := readCertificateEntries(t, path)
	got, _ := entries["bare"]["domains"].([]any)
	if len(got) != 1 || got[0] != "bare.example.com" {
		t.Errorf("domains = %v, want the added list", entries["bare"]["domains"])
	}
}

func TestNormalizeDeclaredDomainsTrimsDedupesAndKeepsOrder(t *testing.T) {
	got := normalizeDeclaredDomains([]string{" a.example.com ", "b.example.com", "a.example.com", "", "   ", "*.b.example.com"})
	want := []string{"a.example.com", "b.example.com", "*.b.example.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (order is the operator's intent)", got, want)
		}
	}
}
