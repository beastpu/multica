package kubefleet

import (
	"encoding/base64"
	"testing"
)

func TestLoadKubeconfig_Token(t *testing.T) {
	// A trivial CA PEM (self-signed) so AppendCertsFromPEM succeeds.
	caPEM := testCAPEM(t)
	yaml := `apiVersion: v1
current-context: ctx
clusters:
- name: c
  cluster:
    server: https://api.example.com:6443
    certificate-authority-data: ` + base64.StdEncoding.EncodeToString(caPEM) + `
users:
- name: u
  user:
    token: abc123
contexts:
- name: ctx
  context:
    cluster: c
    user: u
`
	kc, err := loadKubeconfig(yaml)
	if err != nil {
		t.Fatalf("loadKubeconfig: %v", err)
	}
	if kc.apiURL != "https://api.example.com:6443" {
		t.Fatalf("apiURL = %q", kc.apiURL)
	}
	if kc.bearerToken != "abc123" {
		t.Fatalf("bearerToken = %q", kc.bearerToken)
	}
}

func TestLoadKubeconfig_Errors(t *testing.T) {
	cases := map[string]string{
		"no current-context": `apiVersion: v1
clusters: []`,
		"context not found": `apiVersion: v1
current-context: missing
contexts:
- name: other
  context: {cluster: c, user: u}`,
	}
	for name, yaml := range cases {
		if _, err := loadKubeconfig(yaml); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func testCAPEM(t *testing.T) []byte {
	t.Helper()
	// A minimal well-formed self-signed cert PEM generated once and inlined.
	return []byte(`-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----`)
}
