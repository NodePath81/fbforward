package control

import (
	"bytes"
	"encoding/json"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/notify"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetGeoIPStatusUnavailableWithoutManager(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetGeoIPStatusReturnsResult(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{
		status: geoip.Status{
			ASNDB: geoip.DBStatus{
				Configured:  true,
				Available:   true,
				Path:        "/tmp/asn.mmdb",
				FileModTime: 1712505600,
				FileSize:    1234,
			},
			CountryDB: geoip.DBStatus{},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	result := resp.Result.(map[string]any)
	asn := result["asn_db"].(map[string]any)
	if !asn["configured"].(bool) || !asn["available"].(bool) {
		t.Fatalf("unexpected status payload: %+v", result)
	}
}

func TestGetGeoIPStatusSupportsUnconfiguredAndSingleDBPayloads(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{
		status: geoip.Status{
			ASNDB:     geoip.DBStatus{Configured: true, Path: "/tmp/asn.mmdb"},
			CountryDB: geoip.DBStatus{},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	result := resp.Result.(map[string]any)
	asn := result["asn_db"].(map[string]any)
	country := result["country_db"].(map[string]any)
	if !asn["configured"].(bool) || country["configured"].(bool) {
		t.Fatalf("unexpected single-db payload: %+v", result)
	}

	server.SetGeoIPManager(fakeGeoIPManager{status: geoip.Status{}})
	req = httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec = httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for unconfigured manager, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReloadGeoIPUnavailableWithoutManager(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "ReloadGeoIP", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestReloadGeoIPReturnsStatus(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{status: geoip.Status{ASNDB: geoip.DBStatus{Configured: true, Path: "/tmp/asn.mmdb"}}})

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "ReloadGeoIP", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSendTestNotificationRequiresConfiguredNotifier(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "SendTestNotification", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSendTestNotificationEmitsManualInfoEvent(t *testing.T) {
	server := newTestControlServer(t)
	notifier := &fakeNotifier{accepted: true}
	server.SetNotifier(notifier)

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "SendTestNotification", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if notifier.eventName != "system.test_notification" {
		t.Fatalf("unexpected event name: %q", notifier.eventName)
	}
	if notifier.severity != notify.SeverityInfo {
		t.Fatalf("unexpected severity: %q", notifier.severity)
	}
	if notifier.attributes["test.origin"] != "manual" || notifier.attributes["test.service"] != "fbforward" {
		t.Fatalf("unexpected attributes: %#v", notifier.attributes)
	}
}

func TestSendTestNotificationReturnsErrorWhenQueueRejects(t *testing.T) {
	server := newTestControlServer(t)
	server.SetNotifier(&fakeNotifier{accepted: false})

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "SendTestNotification", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRuntimeConfigSanitizesNotifyConfig(t *testing.T) {
	server := newTestControlServer(t)
	server.fullCfg.Notify = config.NotifyConfig{
		Enabled:            true,
		Endpoint:           "https://notify.example/v1/events",
		BearerToken:        "secret-node-token",
		SourceInstance:     "node-1",
		StartupGracePeriod: config.Duration(10 * time.Minute),
		UnusableInterval:   config.Duration(45 * time.Second),
		NotifyInterval:     config.Duration(2 * time.Hour),
	}

	cfg := server.getRuntimeConfig()
	notifyCfg, ok := cfg["webhook"].(map[string]interface{})
	if !ok {
		t.Fatalf("webhook config missing or wrong type: %#v", cfg["webhook"])
	}
	if notifyCfg["endpoint"] != "https://notify.example/v1/events" {
		t.Fatalf("unexpected notify endpoint: %#v", notifyCfg["endpoint"])
	}
	if _, exists := notifyCfg["bearer_token"]; exists {
		t.Fatalf("unexpected webhook bearer token in runtime config: %#v", notifyCfg)
	}
}
