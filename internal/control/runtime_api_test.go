package control

import (
	"net/http"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
)

func TestReloadGeoIPUnavailableWithoutManager(t *testing.T) {
	server := newTestControlServer(t)
	rec := callTestRPC(t, server, "0123456789abcdef", "ReloadGeoIP", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestReloadGeoIPReturnsStatus(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{status: geoip.Status{ASNDB: geoip.DBStatus{Configured: true, Path: "/tmp/asn.mmdb"}}})

	rec := callTestRPC(t, server, "0123456789abcdef", "ReloadGeoIP", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
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
