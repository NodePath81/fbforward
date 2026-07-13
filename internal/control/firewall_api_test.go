package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/policy"
)

func TestFirewallPolicyRPCs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "firewall.yaml")
	initial := []byte("version: 1\ndefault: deny\nrules: []\n")
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := policy.NewProvider(config.FirewallConfig{Enabled: true, PolicyFile: path}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	server := newTestControlServer(t)
	server.SetFirewallProvider(provider)

	request := func(method string, params any) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, method, params)))
		req.Header.Set("Authorization", "Bearer 0123456789abcdef")
		rec := httptest.NewRecorder()
		server.handleRPC(rec, req)
		return rec
	}

	status := request("GetFirewallStatus", nil)
	if status.Code != http.StatusOK {
		t.Fatalf("GetFirewallStatus: status=%d body=%s", status.Code, status.Body.String())
	}
	var statusResponse rpcResponse
	if err := json.Unmarshal(status.Body.Bytes(), &statusResponse); err != nil {
		t.Fatal(err)
	}
	statusPayload := statusResponse.Result.(map[string]any)
	if statusPayload["state"] != "active" || statusPayload["policy_file"] != path {
		t.Fatalf("unexpected status payload: %#v", statusPayload)
	}

	policyResponse := request("GetFirewallPolicy", nil)
	if policyResponse.Code != http.StatusOK {
		t.Fatalf("GetFirewallPolicy: status=%d body=%s", policyResponse.Code, policyResponse.Body.String())
	}

	valid := request("ValidateFirewallPolicy", map[string]any{
		"content": "version: 1\ndefault: allow\nrules: []\n",
	})
	if valid.Code != http.StatusOK {
		t.Fatalf("ValidateFirewallPolicy: status=%d body=%s", valid.Code, valid.Body.String())
	}
	if provider.Status().Generation != 1 {
		t.Fatalf("validation changed active generation: %+v", provider.Status())
	}

	if err := os.WriteFile(path, []byte("version: 1\ndefault: allow\nrules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded := request("ReloadFirewallPolicy", nil)
	if reloaded.Code != http.StatusOK {
		t.Fatalf("ReloadFirewallPolicy: status=%d body=%s", reloaded.Code, reloaded.Body.String())
	}
	if provider.Status().Generation != 2 {
		t.Fatalf("expected generation 2 after reload: %+v", provider.Status())
	}

	if err := os.WriteFile(path, []byte("version: 1\ndefault: invalid\nrules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := request("ReloadFirewallPolicy", nil)
	if failed.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid reload, got %d body=%s", failed.Code, failed.Body.String())
	}
	if provider.Status().Generation != 2 {
		t.Fatalf("failed reload changed active generation: %+v", provider.Status())
	}
}

func TestFirewallRPCRequiresControlAuthentication(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetFirewallStatus", nil)))
	rec := httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}
